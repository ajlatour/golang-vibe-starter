package main

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// A Kind says what a change means to a client written against the old spec.
type Kind int

const (
	// Additive: a client written against the old spec keeps working.
	Additive Kind = iota
	// Breaking: some client written against the old spec may stop working.
	Breaking
)

func (k Kind) String() string {
	if k == Breaking {
		return "breaking"
	}
	return "additive"
}

// A Change is one difference between two specs.
type Change struct {
	Kind  Kind
	Where string // e.g. "GET /items/{id}: response 200: body.name"
	What  string
}

// spec is an OpenAPI document, decoded, with its $refs resolved on demand.
type spec struct{ doc map[string]any }

// resolve follows v's $ref, if it has one, within s.
func (s spec) resolve(v any) map[string]any {
	m, _ := v.(map[string]any)
	for range 20 { // a cycle of refs is a broken spec; stop
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		var cur any = s.doc
		for part := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
			cm, _ := cur.(map[string]any)
			cur = cm[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
		}
		m, _ = cur.(map[string]any)
	}
	return m
}

var methods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

// operations maps "GET /items" to its operation, and its path item (for the
// parameters every operation on the path shares).
func (s spec) operations() map[string][2]map[string]any {
	out := map[string][2]map[string]any{}
	paths, _ := s.doc["paths"].(map[string]any)
	for path, item := range paths {
		pi, _ := item.(map[string]any)
		for _, m := range methods {
			if op, ok := pi[m].(map[string]any); ok {
				out[strings.ToUpper(m)+" "+path] = [2]map[string]any{op, pi}
			}
		}
	}
	return out
}

// Diff lists what changed from old to new, breaking changes first.
func Diff(oldDoc, newDoc map[string]any) []Change {
	d := differ{old: spec{oldDoc}, new: spec{newDoc}}
	oldOps, newOps := d.old.operations(), d.new.operations()
	for _, key := range sortedKeys(oldOps) {
		n, ok := newOps[key]
		if !ok {
			d.add(Breaking, key, "the operation was removed")
			continue
		}
		d.operation(key, oldOps[key], n)
	}
	for _, key := range sortedKeys(newOps) {
		if _, ok := oldOps[key]; !ok {
			d.add(Additive, key, "a new operation")
		}
	}
	sort.SliceStable(d.changes, func(i, j int) bool { return d.changes[i].Kind > d.changes[j].Kind })
	return d.changes
}

type differ struct {
	old, new spec
	changes  []Change
}

func (d *differ) add(k Kind, where, what string, args ...any) {
	d.changes = append(d.changes, Change{Kind: k, Where: where, What: fmt.Sprintf(what, args...)})
}

func (d *differ) operation(key string, o, n [2]map[string]any) {
	// Parameters, keyed by where they go and their name.
	params := func(s spec, op [2]map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, list := range []any{op[1]["parameters"], op[0]["parameters"]} {
			items, _ := list.([]any)
			for _, p := range items {
				pm := s.resolve(p)
				name, _ := pm["name"].(string)
				in, _ := pm["in"].(string)
				out[in+" "+name] = pm
			}
		}
		return out
	}
	op, np := params(d.old, o), params(d.new, n)
	for _, k := range sortedKeys(np) {
		where := key + ": parameter " + k
		p := np[k]
		required, _ := p["required"].(bool)
		old, existed := op[k]
		wasRequired, _ := old["required"].(bool)
		switch {
		case !existed && required:
			d.add(Breaking, where, "a new required parameter")
		case !existed:
			d.add(Additive, where, "a new optional parameter")
		case required && !wasRequired:
			d.add(Breaking, where, "the parameter became required")
		}
		if existed {
			d.schema(where, d.old.resolve(old["schema"]), d.new.resolve(p["schema"]), request)
		}
	}
	for _, k := range sortedKeys(op) {
		if _, ok := np[k]; !ok {
			// A client that still sends it is ignored, not refused.
			d.add(Additive, key+": parameter "+k, "the parameter was removed: it's ignored now")
		}
	}

	// The request body.
	ob, nb := d.old.resolve(o[0]["requestBody"]), d.new.resolve(n[0]["requestBody"])
	oReq, _ := ob["required"].(bool)
	nReq, _ := nb["required"].(bool)
	if nb != nil && nReq && (ob == nil || !oReq) {
		d.add(Breaking, key+": request body", "a body became required")
	}
	if ob != nil && nb != nil {
		d.schema(key+": request body", d.jsonSchema(d.old, ob), d.jsonSchema(d.new, nb), request)
	}

	// The responses.
	ors, _ := o[0]["responses"].(map[string]any)
	nrs, _ := n[0]["responses"].(map[string]any)
	for _, status := range sortedKeys(ors) {
		where := key + ": response " + status
		nr, ok := nrs[status]
		if !ok {
			if isSuccess(status) {
				d.add(Breaking, where, "a success status was removed")
			} else {
				d.add(Additive, where, "an error status is no longer documented")
			}
			continue
		}
		orr, nrr := d.old.resolve(ors[status]), d.new.resolve(nr)
		d.schema(where+": body", d.jsonSchema(d.old, orr), d.jsonSchema(d.new, nrr), response)
		if isSuccess(status) {
			oh, _ := orr["headers"].(map[string]any)
			nh, _ := nrr["headers"].(map[string]any)
			for _, h := range sortedKeys(oh) {
				if _, ok := nh[h]; !ok {
					d.add(Breaking, where+": header "+h, "a documented response header was removed")
				}
			}
		}
	}
	for _, status := range sortedKeys(nrs) {
		if _, ok := ors[status]; !ok {
			d.add(Additive, key+": response "+status, "a new status")
		}
	}
}

func isSuccess(status string) bool {
	n, err := strconv.Atoi(status)
	return err == nil && n >= 200 && n < 300
}

// jsonSchema is a body's or response's application/json schema, resolved.
func (d *differ) jsonSchema(s spec, bodyOrResponse map[string]any) map[string]any {
	content, _ := bodyOrResponse["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	if media == nil {
		return nil
	}
	return s.resolve(media["schema"])
}

// direction is which way a schema's data flows: what a client sends, whose
// tightening breaks it, or what it receives, whose loss does.
type direction int

const (
	request direction = iota
	response
)

// schema compares two schemas for the same data.
func (d *differ) schema(where string, o, n map[string]any, dir direction) {
	if o == nil || n == nil {
		if o != nil && dir == response {
			d.add(Breaking, where, "the body was removed")
		}
		return
	}
	if ot, nt := o["type"], n["type"]; ot != nil && nt != nil && ot != nt {
		d.add(Breaking, where, "the type changed from %v to %v", ot, nt)
		return
	}

	// Enums.
	oe, ne := strings_(o["enum"]), strings_(n["enum"])
	switch {
	case dir == request && len(ne) > 0 && len(oe) == 0:
		d.add(Breaking, where, "values are now limited to %v", ne)
	case dir == request:
		for _, v := range oe {
			if !slices.Contains(ne, v) {
				d.add(Breaking, where, "the value %q is no longer accepted", v)
			}
		}
	case dir == response:
		for _, v := range ne {
			if len(oe) > 0 && !slices.Contains(oe, v) {
				d.add(Additive, where, "a new value, %q", v)
			}
		}
	}

	// Constraints on what a client sends.
	if dir == request {
		tighter := func(key string, lower bool) {
			ov, oOK := o[key].(float64)
			nv, nOK := n[key].(float64)
			if nOK && (!oOK || (lower && nv < ov) || (!lower && nv > ov)) {
				d.add(Breaking, where, "%s is now %v (was %v)", key, nv, describe(o[key]))
			}
		}
		tighter("maxLength", true)
		tighter("maximum", true)
		tighter("maxItems", true)
		tighter("minLength", false)
		tighter("minimum", false)
		tighter("minItems", false)
		if np, ok := n["pattern"].(string); ok && np != o["pattern"] {
			d.add(Breaking, where, "the pattern is now %q (was %v)", np, describe(o["pattern"]))
		}
	}

	// Properties.
	oProps, _ := o["properties"].(map[string]any)
	nProps, _ := n["properties"].(map[string]any)
	oRequired, nRequired := strings_(o["required"]), strings_(n["required"])
	for _, name := range sortedKeys(oProps) {
		at := where + "." + name
		np, ok := nProps[name]
		if !ok {
			switch {
			case dir == response:
				d.add(Breaking, at, "the field was removed")
			case n["additionalProperties"] == false:
				d.add(Breaking, at, "the field was removed, and unknown fields are refused")
			}
			continue
		}
		switch {
		case dir == request && slices.Contains(nRequired, name) && !slices.Contains(oRequired, name):
			d.add(Breaking, at, "the field became required")
		case dir == response && slices.Contains(oRequired, name) && !slices.Contains(nRequired, name):
			d.add(Breaking, at, "the field may now be absent")
		}
		d.schema(at, d.old.resolve(oProps[name]), d.new.resolve(np), dir)
	}
	for _, name := range sortedKeys(nProps) {
		if _, ok := oProps[name]; ok {
			continue
		}
		at := where + "." + name
		if dir == request && slices.Contains(nRequired, name) {
			d.add(Breaking, at, "a new required field")
		} else {
			d.add(Additive, at, "a new field")
		}
	}
	if oi, ni := d.old.resolve(o["items"]), d.new.resolve(n["items"]); oi != nil && ni != nil {
		d.schema(where+"[]", oi, ni, dir)
	}
}

func strings_(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, x := range list {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func describe(v any) string {
	if v == nil {
		return "none"
	}
	return fmt.Sprint(v)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// RequiredBump is the smallest version bump that may carry changes:
// "major" for a breaking change from 1.0.0 on, "minor" for one before it
// (0.x may break callers in a minor release) or for anything added, else
// "patch".
func RequiredBump(changes []Change, from [3]int) string {
	bump := "patch"
	for _, c := range changes {
		switch {
		case c.Kind == Breaking && from[0] >= 1:
			return "major"
		case c.Kind == Breaking, c.Kind == Additive:
			bump = "minor"
		}
	}
	return bump
}

// Bump is the bump from one version to the next: "major", "minor", "patch",
// or "" if to isn't later than from.
func Bump(from, to [3]int) string {
	switch {
	case to[0] > from[0]:
		return "major"
	case to[0] == from[0] && to[1] > from[1]:
		return "minor"
	case to[0] == from[0] && to[1] == from[1] && to[2] > from[2]:
		return "patch"
	}
	return ""
}

// ParseVersion reads vMAJOR.MINOR.PATCH.
func ParseVersion(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 || !strings.HasPrefix(v, "v") {
		return out, fmt.Errorf("version %q: want vMAJOR.MINOR.PATCH", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("version %q: want vMAJOR.MINOR.PATCH", v)
		}
		out[i] = n
	}
	return out, nil
}

// Enough reports whether a bump covers what's required of it.
func Enough(bump, required string) bool {
	rank := map[string]int{"": -1, "patch": 0, "minor": 1, "major": 2}
	return rank[bump] >= rank[required]
}
