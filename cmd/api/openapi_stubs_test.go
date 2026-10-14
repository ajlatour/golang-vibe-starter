package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The WireMock stubs (wiremock/) stand in for the upstream, which speaks the
// API's own /items contract (httpstore). Every test that runs against the
// stack trusts them, so these tests hold them to docs/openapi.json: a stub
// that answered in a shape the contract doesn't allow would make the
// integration tests pass against an upstream that can't exist.
//
// The upstream's errors are its own: {"error": "..."} without the API's code,
// which httpstore quotes in its logs, and statuses the API doesn't document
// (a 405 from its router). So only its successes are held to the spec's
// schemas; its errors to the shape httpstore reads.

// stubMapping is the part of a WireMock mapping these tests read.
type stubMapping struct {
	Name    string
	Request struct {
		Method         string
		URL            string `json:"url"`
		URLPath        string `json:"urlPath"`
		URLPattern     string `json:"urlPattern"`
		URLPathPattern string `json:"urlPathPattern"`
	}
	Response struct {
		Status       int
		Fault        string
		Headers      map[string]string
		Body         string
		JSONBody     any `json:"jsonBody"`
		Transformers []string
	}
	file string // relative to wiremock/
}

func (m stubMapping) String() string { return fmt.Sprintf("%s: %q", m.file, m.Name) }

func loadStubs(t *testing.T) []stubMapping {
	t.Helper()
	files, err := filepath.Glob("../../wiremock/*/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no stubs in wiremock/: %v", err)
	}
	var all []stubMapping
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// A file holds one mapping, or several under "mappings".
		var doc struct{ Mappings []stubMapping }
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if doc.Mappings == nil {
			var one stubMapping
			if err := json.Unmarshal(b, &one); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			doc.Mappings = []stubMapping{one}
		}
		rel, _ := filepath.Rel("../../wiremock", f)
		for _, m := range doc.Mappings {
			m.file = filepath.ToSlash(rel)
			all = append(all, m)
		}
	}
	return all
}

// stubSamplePaths are concrete paths for each of the contract's paths, for
// trying a stub's URL pattern on: a pattern is only known by what it matches.
// An item's are any ID, and those the stubs know, which a pattern may list.
var stubSamplePaths = map[string][]string{
	"/items":      {"/items"},
	"/items/{id}": {"/items/sample-id", "/items/widget", "/items/gadget", "/items/gizmo", "/items/new-item"},
	"/healthz":    {"/healthz"},
	"/readyz":     {"/readyz"},
}

// notTheItemsUpstream are paths stubbed for other services on the same
// WireMock: the validation service (internal/validator).
var notTheItemsUpstream = []string{"/validate"}

// paths returns the contract's paths m's URL matcher matches. An exact URL
// is matched against each path's template; a pattern, by trying it on each
// path's samples.
func (m stubMapping) paths(t *testing.T) []string {
	t.Helper()
	r := m.Request
	exact, _, _ := strings.Cut(r.URL+r.URLPath, "?")
	var pattern *regexp.Regexp
	if p := r.URLPathPattern + r.URLPattern; p != "" {
		var err error
		if pattern, err = regexp.Compile("^(?:" + p + ")$"); err != nil {
			t.Fatalf("%v: %v", m, err)
		}
	}
	var out []string
	for path, samples := range stubSamplePaths {
		template := regexp.MustCompile("^" + regexp.MustCompile(`\{[^/]+\}`).ReplaceAllString(path, "[^/]+") + "$")
		if (exact != "" && template.MatchString(exact)) || (pattern != nil && slices.ContainsFunc(samples, pattern.MatchString)) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// isOtherService reports whether m stubs one of notTheItemsUpstream.
func (m stubMapping) isOtherService() bool {
	r := m.Request
	return slices.Contains(notTheItemsUpstream, r.URL) || slices.Contains(notTheItemsUpstream, r.URLPath)
}

// templateRE finds the response-template helpers the write stubs use, which
// render to a JSON value (toJson) or a time (now). Anything else in {{ }}
// fails the test: teach it here, or its body goes unchecked.
var (
	templateJSONRE = regexp.MustCompile(`\{\{toJson [^{}]*\}\}`)
	templateNowRE  = regexp.MustCompile(`\{\{now\}\}`)
)

// body returns m's response body as decoded JSON, rendering a templated one
// with sample values, and whether there is one.
func (m stubMapping) body(t *testing.T) (any, bool) {
	t.Helper()
	resp := m.Response
	if resp.JSONBody != nil {
		return resp.JSONBody, true
	}
	if resp.Body == "" {
		return nil, false
	}
	b := resp.Body
	if slices.Contains(resp.Transformers, "response-template") {
		b = templateJSONRE.ReplaceAllString(b, `"Sample"`)
		b = templateNowRE.ReplaceAllString(b, "2026-01-01T00:00:00Z")
		if strings.Contains(b, "{{") {
			t.Errorf("%v: a template helper this test can't render; teach it (openapi_stubs_test.go): %s", m, b)
			return nil, false
		}
	}
	var v any
	if err := json.Unmarshal([]byte(b), &v); err != nil {
		return nil, false
	}
	return v, true
}

func (m stubMapping) isJSON() bool {
	mt, _, _ := mime.ParseMediaType(m.Response.Headers["Content-Type"])
	return mt == "application/json"
}

// deliberatelyBroken are stubs whose body is wrong on purpose, for the
// fault scenarios: their routes and statuses are still checked.
var deliberatelyBroken = []string{"faults/malformed-list.json"}

func TestWireMockStubsMatchTheContract(t *testing.T) {
	s := loadSpec(t)
	ops := map[string]operation{} // by Key
	for _, o := range s.operations(t) {
		ops[o.Key()] = o
	}

	for _, m := range loadStubs(t) {
		if m.isOtherService() {
			continue
		}
		t.Run(m.file+"/"+m.Name, func(t *testing.T) {
			paths := m.paths(t)
			if len(paths) == 0 {
				t.Fatalf("matches no path of the contract (%s); a typo, or a service to add to notTheItemsUpstream", strings.Join(slices.Sorted(maps.Keys(stubSamplePaths)), ", "))
			}
			// The operations the stub can answer: its method on its paths,
			// or for ANY, every method there is on them.
			var matched []operation
			for _, p := range paths {
				for _, method := range httpMethods {
					o, ok := ops[strings.ToUpper(method)+" "+p]
					if ok && (m.Request.Method == "ANY" || strings.EqualFold(m.Request.Method, method)) {
						matched = append(matched, o)
					}
				}
			}
			if len(matched) == 0 {
				t.Fatalf("%s on %v: no such operation in the contract", m.Request.Method, paths)
			}
			if m.Response.Fault != "" {
				return // no response to check: the connection fails
			}
			status := m.Response.Status
			body, hasBody := m.body(t)

			if status >= 200 && status < 300 {
				for _, o := range matched {
					want := successStatus(t, s, o)
					if status != want {
						t.Errorf("%s answers %d; the contract's success is %d", o.Key(), status, want)
						continue
					}
					if slices.Contains(deliberatelyBroken, m.file) {
						continue
					}
					schema := s.jsonSchema(t, s.responses(t, o)[status])
					switch {
					case schema == nil && hasBody:
						t.Errorf("%s %d has a body; the contract has none", o.Key(), status)
					case schema != nil && !hasBody:
						t.Errorf("%s %d: the body isn't JSON, or there's none", o.Key(), status)
					case schema != nil:
						if !m.isJSON() {
							t.Errorf("Content-Type %q, want application/json", m.Response.Headers["Content-Type"])
						}
						for _, e := range s.validate(t, schema, body, "body") {
							t.Errorf("%s %d: %s", o.Key(), status, e)
						}
					}
				}
				return
			}

			// An error. httpstore reads a JSON 404 as "no such item", and
			// any other 404 as a missing route (a misconfigured base URL).
			if status == 404 && !m.isJSON() {
				t.Errorf("a 404 that isn't JSON says the route is missing, not the item")
			}
			if m.isJSON() {
				obj, ok := body.(map[string]any)
				msg, isString := obj["error"].(string)
				if !ok || len(obj) != 1 || !isString || msg == "" {
					t.Errorf(`an upstream error body is {"error": "<message>"}, which httpstore quotes; got %v`, body)
				}
			}
		})
	}
}

// Every operation of each resource's contract has its success stubbed among
// the happy-path stubs (wiremock/mappings), and each one on an entity its 404.
func TestWireMockStubsCoverTheContract(t *testing.T) {
	s := loadSpec(t)
	answered := map[string]bool{} // "GET /items/{id} 200"
	for _, m := range loadStubs(t) {
		if !strings.HasPrefix(m.file, "mappings/") || m.isOtherService() || m.Response.Fault != "" {
			continue
		}
		for _, p := range m.paths(t) {
			for _, method := range httpMethods {
				if m.Request.Method == "ANY" || strings.EqualFold(m.Request.Method, method) {
					answered[fmt.Sprintf("%s %s %d", strings.ToUpper(method), p, m.Response.Status)] = true
				}
			}
		}
	}
	for _, o := range s.operations(t) {
		if o.Path == "/healthz" || o.Path == "/readyz" {
			continue // the API's own probes; the upstream's aren't stubbed
		}
		want := []int{successStatus(t, s, o)}
		if strings.Contains(o.Path, "{id}") {
			want = append(want, 404)
		}
		for _, status := range want {
			if !answered[fmt.Sprintf("%s %d", o.Key(), status)] {
				t.Errorf("no stub in wiremock/mappings answers %s with %d", o.Key(), status)
			}
		}
	}
}

// successStatus is the operation's one 2xx response.
func successStatus(t *testing.T, s *spec, o operation) int {
	t.Helper()
	var ok []int
	for status := range s.responses(t, o) {
		if status >= 200 && status < 300 {
			ok = append(ok, status)
		}
	}
	if len(ok) != 1 {
		t.Fatalf("%s: %d success statuses %v; the stub tests expect one", o.Key(), len(ok), ok)
	}
	return ok[0]
}
