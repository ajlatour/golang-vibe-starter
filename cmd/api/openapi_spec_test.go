package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Helpers for the OpenAPI tests: load docs/openapi.json, follow $refs, and
// validate JSON values against the spec's schemas. The project is standard
// library only, so this is a small validator for the subset of JSON Schema the
// spec uses. It refuses any keyword it doesn't understand, so a schema can
// never be silently under-checked.

type spec struct{ doc map[string]any }

func loadSpec(t *testing.T) *spec {
	t.Helper()
	b, err := os.ReadFile("../../docs/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("docs/openapi.json is not valid JSON: %v", err)
	}
	return &spec{doc: doc}
}

// lookup follows a local reference such as "#/components/schemas/Item".
func (s *spec) lookup(ref string) (map[string]any, bool) {
	path, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil, false
	}
	var cur any = s.doc
	for part := range strings.SplitSeq(path, "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	m, ok := cur.(map[string]any)
	return m, ok
}

// resolve returns v with any $ref followed. It fails the lookup rather than
// returning something half-resolved.
func (s *spec) resolve(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected an object in the spec, got %T", v)
	}
	for range 10 {
		ref, isRef := m["$ref"].(string)
		if !isRef {
			return m
		}
		next, ok := s.lookup(ref)
		if !ok {
			t.Fatalf("spec $ref %q does not resolve", ref)
		}
		m = next
	}
	t.Fatal("spec $ref chain is too deep (a cycle?)")
	return nil
}

// operation is one method on one path.
type operation struct {
	Path, Method string
	Op           map[string]any
}

// Key is the operation as a ServeMux pattern: "GET /items/{id}".
func (o operation) Key() string { return strings.ToUpper(o.Method) + " " + o.Path }

func (o operation) ID() string { s, _ := o.Op["operationId"].(string); return s }

var httpMethods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

func (s *spec) operations(t *testing.T) []operation {
	t.Helper()
	paths, _ := s.doc["paths"].(map[string]any)
	var ops []operation
	for path, item := range paths {
		for _, method := range httpMethods {
			if op, ok := item.(map[string]any)[method]; ok {
				ops = append(ops, operation{Path: path, Method: method, Op: op.(map[string]any)})
			}
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Key() < ops[j].Key() })
	return ops
}

// parameters returns the operation's resolved parameters: the path item's and
// its own.
func (s *spec) parameters(t *testing.T, o operation) []map[string]any {
	t.Helper()
	paths := s.doc["paths"].(map[string]any)
	var out []map[string]any
	for _, list := range []any{paths[o.Path].(map[string]any)["parameters"], o.Op["parameters"]} {
		items, _ := list.([]any)
		for _, p := range items {
			out = append(out, s.resolve(t, p))
		}
	}
	return out
}

// responses returns the operation's resolved responses by status.
func (s *spec) responses(t *testing.T, o operation) map[int]map[string]any {
	t.Helper()
	out := map[int]map[string]any{}
	raw, _ := o.Op["responses"].(map[string]any)
	for code, r := range raw {
		n, err := strconv.Atoi(code)
		if err != nil {
			t.Fatalf("%s: response key %q is not a status code", o.Key(), code)
		}
		out[n] = s.resolve(t, r)
	}
	return out
}

// jsonSchema returns the application/json schema of a response or request
// body, or nil if it has none.
func (s *spec) jsonSchema(t *testing.T, bodyOrResponse map[string]any) map[string]any {
	t.Helper()
	content, _ := bodyOrResponse["content"].(map[string]any)
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil
	}
	return media["schema"].(map[string]any)
}

// schemaKeywords are the JSON Schema keywords validate understands. Anything
// else in a schema fails the test, so the spec can't grow a constraint that
// nothing checks.
var schemaKeywords = map[string]bool{
	"$ref": true, "type": true, "properties": true, "required": true,
	"additionalProperties": true, "items": true, "minLength": true,
	"maxLength": true, "pattern": true, "format": true, "enum": true,
	"minimum": true,
	// Annotations: documentation only.
	"description": true, "example": true, "title": true,
}

// validate checks v (decoded JSON) against schema and returns what's wrong,
// each prefixed with where in the value it was found.
func (s *spec) validate(t *testing.T, schema map[string]any, v any, at string) []string {
	t.Helper()
	schema = s.resolve(t, schema)
	var errs []string
	for k := range schema {
		if !schemaKeywords[k] {
			errs = append(errs, fmt.Sprintf("%s: the test validator doesn't support the schema keyword %q; teach it, or the spec is under-checked", at, k))
		}
	}

	if typ, ok := schema["type"].(string); ok && !isType(typ, v) {
		return append(errs, fmt.Sprintf("%s: want %s, got %s", at, typ, describe(v)))
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			found = found || e == v
		}
		if !found {
			errs = append(errs, fmt.Sprintf("%s: %v is not one of %v", at, v, enum))
		}
	}

	switch x := v.(type) {
	case string:
		n := utf8.RuneCountInString(x)
		if min, ok := schema["minLength"].(float64); ok && float64(n) < min {
			errs = append(errs, fmt.Sprintf("%s: %d characters, below minLength %v", at, n, min))
		}
		if max, ok := schema["maxLength"].(float64); ok && float64(n) > max {
			errs = append(errs, fmt.Sprintf("%s: %d characters, above maxLength %v", at, n, max))
		}
		if p, ok := schema["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(x) {
			errs = append(errs, fmt.Sprintf("%s: %q does not match pattern %q", at, truncate(x), p))
		}
		if f, ok := schema["format"].(string); ok {
			if f != "date-time" {
				errs = append(errs, fmt.Sprintf("%s: the test validator doesn't support format %q", at, f))
			} else if _, err := time.Parse(time.RFC3339, x); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %q is not an RFC 3339 date-time", at, truncate(x)))
			}
		}
	case float64:
		if min, ok := schema["minimum"].(float64); ok && x < min {
			errs = append(errs, fmt.Sprintf("%s: %v is below minimum %v", at, x, min))
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, e := range x {
				errs = append(errs, s.validate(t, items, e, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, present := x[r.(string)]; !present {
					errs = append(errs, fmt.Sprintf("%s: missing required property %q", at, r))
				}
			}
		}
		for name, val := range x {
			p, known := props[name]
			if !known {
				if extra, ok := schema["additionalProperties"].(bool); ok && !extra {
					errs = append(errs, fmt.Sprintf("%s: unexpected property %q", at, name))
				}
				continue
			}
			errs = append(errs, s.validate(t, p.(map[string]any), val, at+"."+name)...)
		}
	}
	return errs
}

func isType(typ string, v any) bool {
	switch typ {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	case "null":
		return v == nil
	}
	return false
}

func describe(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case string:
		return "a string"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) > 40 {
		return string([]rune(s)[:40]) + "…"
	}
	return s
}

// walkRefs calls fn with every $ref value in the document.
func walkRefs(v any, fn func(ref string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if ref, ok := val.(string); ok && k == "$ref" {
				fn(ref)
				continue
			}
			walkRefs(val, fn)
		}
	case []any:
		for _, e := range x {
			walkRefs(e, fn)
		}
	}
}
