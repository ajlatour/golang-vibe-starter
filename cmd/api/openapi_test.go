package main

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// These tests keep docs/openapi.json honest. They run the real handlers and
// compare what they do with what the spec says: the routes, the status codes,
// the response bodies and headers, and the limits. Change the API or the spec
// without the other and a test fails, saying which one to update.
//
// They're spread over a few files: this one checks the spec is well formed
// and holds every response to it (TestOpenAPIMatchesBehavior);
// openapi_routes_test.go matches the spec's routes with the code's;
// openapi_scenarios_test.go lists the requests to make, and
// openapi_envs_test.go the services to make them to; openapi_params_test.go
// checks the item schema and the parameters' rules; and openapi_spec_test.go
// reads the spec and validates against it.

// TestOpenAPISpecIsWellFormed lints the spec itself. There's no third-party
// OpenAPI validator in a standard-library-only project, so this checks what
// the other tests rely on: that it's OpenAPI 3, every $ref resolves, every
// operation is identified and has responses, and path parameters are declared.
func TestOpenAPISpecIsWellFormed(t *testing.T) {
	s := loadSpec(t)

	if v, _ := s.doc["openapi"].(string); !strings.HasPrefix(v, "3.") {
		t.Errorf("openapi = %q, want a 3.x version", v)
	}
	info, _ := s.doc["info"].(map[string]any)
	for _, k := range []string{"title", "version"} {
		if v, _ := info[k].(string); v == "" {
			t.Errorf("info.%s is empty", k)
		}
	}
	walkRefs(s.doc, func(ref string) {
		if _, ok := s.lookup(ref); !ok {
			t.Errorf("$ref %q does not resolve", ref)
		}
	})

	ids := map[string]string{}
	ops := s.operations(t)
	if len(ops) == 0 {
		t.Fatal("the spec has no operations")
	}
	for _, o := range ops {
		if o.ID() == "" {
			t.Errorf("%s has no operationId", o.Key())
		} else if prev, dup := ids[o.ID()]; dup {
			t.Errorf("operationId %q is used by both %s and %s", o.ID(), prev, o.Key())
		}
		ids[o.ID()] = o.Key()

		if len(s.responses(t, o)) == 0 {
			t.Errorf("%s documents no responses", o.Key())
		}
		if body, ok := o.Op["requestBody"]; ok {
			if s.jsonSchema(t, s.resolve(t, body)) == nil {
				t.Errorf("%s has a request body without an application/json schema", o.Key())
			}
		}

		// Every {param} in the path is declared as a required path parameter,
		// and every declared path parameter appears in the path.
		inPath := map[string]bool{}
		for _, m := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(o.Path, -1) {
			inPath[m[1]] = true
		}
		declared := map[string]bool{}
		for _, p := range s.parameters(t, o) {
			if p["in"] != "path" {
				continue
			}
			name, _ := p["name"].(string)
			declared[name] = true
			if req, _ := p["required"].(bool); !req {
				t.Errorf("%s: path parameter %q must be required", o.Key(), name)
			}
		}
		for name := range inPath {
			if !declared[name] {
				t.Errorf("%s: {%s} is not declared as a path parameter", o.Key(), name)
			}
		}
		for name := range declared {
			if !inPath[name] {
				t.Errorf("%s: path parameter %q is not in the path", o.Key(), name)
			}
		}
	}
}

// seededID is the ID of the one item the "ok" environments start with.
const seededPlaceholder = "{seeded}"

// lookupSchema returns a named schema from components.
func (s *spec) lookupSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	m, ok := s.lookup("#/components/schemas/" + name)
	if !ok {
		t.Fatalf("spec has no schema %q", name)
	}
	return m
}

// TestOpenAPIMatchesBehavior sends each scenario to the real handlers and
// checks the answer against the spec: the status is documented, the body
// matches the response schema, the documented headers are present, and every
// documented status is actually produced by some scenario.
func TestOpenAPIMatchesBehavior(t *testing.T) {
	s := loadSpec(t)
	ops := map[string]operation{}
	for _, o := range s.operations(t) {
		ops[o.Key()] = o
	}
	observed := map[string]map[int]bool{}

	for _, sc := range scenarios(t, s) {
		t.Run(sc.name, func(t *testing.T) {
			o, ok := ops[sc.op]
			if !ok {
				t.Fatalf("scenario targets %q, which is not in the spec", sc.op)
			}
			handler, seeded := handlerFor(t, sc.env)
			path := strings.ReplaceAll(sc.path, seededPlaceholder, seeded)

			// The body's verdict matches the spec's: 400 and 422 are the
			// service refusing the body, and exactly the bodies the schema
			// calls invalid get them. Any other status (a 404 for a missing
			// item, a 502 from the upstream) means the body itself was fine.
			if rb, ok := o.Op["requestBody"]; ok && sc.body != "" {
				var decoded any
				if json.Unmarshal([]byte(sc.body), &decoded) == nil {
					bad := s.validate(t, s.jsonSchema(t, s.resolve(t, rb)), decoded, "request")
					refused := sc.want == 400 || sc.want == 422
					if !refused && len(bad) > 0 {
						t.Errorf("the spec says this request body is invalid, but the scenario expects %d: %v", sc.want, bad)
					}
					if refused && len(bad) == 0 && sc.exempt == "" {
						t.Errorf("the spec says this request body is valid, but the service refuses it with %d", sc.want)
					}
				}
			}

			req := httptest.NewRequest(sc.method, path, strings.NewReader(sc.body))
			if sc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Authorization", "Bearer "+testWriteKey)
			for name, values := range sc.header {
				if values == nil {
					req.Header.Del(name) // nil means: don't send it
					continue
				}
				req.Header[name] = values
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != sc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, sc.want, rec.Body)
			}
			if observed[sc.op] == nil {
				observed[sc.op] = map[int]bool{}
			}
			observed[sc.op][rec.Code] = true

			resp, documented := s.responses(t, o)[rec.Code]
			if !documented {
				t.Fatalf("%s answered %d, which the spec doesn't document for it", sc.op, rec.Code)
			}

			// Body: matches the schema, or is empty if the response has none.
			if schema := s.jsonSchema(t, resp); schema != nil {
				if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", ct)
				}
				var body any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body is not JSON: %v: %s", err, rec.Body)
				}
				for _, bad := range s.validate(t, schema, body, "response") {
					t.Errorf("body doesn't match the spec: %s (body: %s)", bad, rec.Body)
				}
			} else if rec.Body.Len() != 0 {
				t.Errorf("the spec documents no body for %s %d, but one was sent: %s", sc.op, rec.Code, rec.Body)
			}

			// Headers: every documented required header is present and fits
			// its schema.
			headers, _ := resp["headers"].(map[string]any)
			for name, h := range headers {
				h := s.resolve(t, h)
				got := rec.Header().Get(name)
				if req, _ := h["required"].(bool); req && got == "" {
					t.Errorf("the spec requires header %s on %s %d, but it is missing", name, sc.op, rec.Code)
				}
				if got != "" {
					if schema, ok := h["schema"].(map[string]any); ok {
						for _, bad := range s.validate(t, schema, got, "header "+name) {
							t.Error(bad)
						}
					}
				}
			}
			if sc.want == 201 {
				var created struct{ ID string }
				json.Unmarshal(rec.Body.Bytes(), &created)
				if loc := rec.Header().Get("Location"); loc != "/items/"+created.ID {
					t.Errorf("Location = %q, want /items/%s", loc, created.ID)
				}
			}
			if sc.check != nil {
				sc.check(t, rec.Body.Bytes())
			}
		})
	}

	// Every documented response must be reachable.
	for key, o := range ops {
		for code := range s.responses(t, o) {
			if !observed[key][code] {
				t.Errorf("the spec documents %d for %s, but no scenario produces it: remove it from the spec, or add a scenario", code, key)
			}
		}
	}
}
