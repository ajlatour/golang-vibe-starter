package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
)

// These tests keep docs/openapi.json honest. They run the real handlers and
// compare what they do with what the spec says: the routes, the status codes,
// the response bodies and headers, and the limits. Change the API or the spec
// without the other and a test fails, saying which one to update.

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

// codeRoutes returns every route pattern the non-test code registers on a
// ServeMux ("GET /items/{id}"), found by reading the source. It skips
// internal/profiling, whose routes are served on a listener of their own
// (PPROF_ADDR), not the API's.
func codeRoutes(t *testing.T) map[string]bool {
	t.Helper()
	routeRE := regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS) /`)
	fset := token.NewFileSet()
	routes := map[string]bool{}
	for _, root := range []string{"../../internal", "."} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && path == filepath.Join("..", "..", "internal", "profiling") {
				return filepath.SkipDir
			}
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
					return true
				}
				switch arg := call.Args[0].(type) {
				case *ast.BasicLit: // mux.Handle("GET /items", ...)
					if p, err := strconv.Unquote(arg.Value); err == nil && routeRE.MatchString(p) {
						routes[p] = true
					}
				case *ast.BinaryExpr: // mux.HandleFunc("GET "+httpx.HealthzPath, ...)
					left, lok := arg.X.(*ast.BasicLit)
					right, rok := arg.Y.(*ast.SelectorExpr)
					if lok && rok && right.Sel.Name == "HealthzPath" {
						method, _ := strconv.Unquote(left.Value)
						routes[method+httpx.HealthzPath] = true
					} else if lok {
						t.Errorf("%s: can't read this route pattern; teach codeRoutes about it so the spec check covers it", fset.Position(arg.Pos()))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return routes
}

// TestOpenAPIRoutesMatchCode fails if a route the code serves isn't in the
// spec, or a route in the spec isn't served.
func TestOpenAPIRoutesMatchCode(t *testing.T) {
	s := loadSpec(t)
	documented := map[string]bool{}
	for _, o := range s.operations(t) {
		documented[o.Key()] = true
	}
	code := codeRoutes(t)
	if len(code) == 0 {
		t.Fatal("found no routes in the code; the scanner is broken")
	}
	for r := range code {
		if !documented[r] {
			t.Errorf("the code serves %q, which is not in docs/openapi.json: document it", r)
		}
	}
	for r := range documented {
		if !code[r] {
			t.Errorf("docs/openapi.json documents %q, which the code doesn't serve: remove it or implement it", r)
		}
	}
}

// fixedFlags is an item.Flags with one answer for every flag.
type fixedFlags bool

func (f fixedFlags) Bool(context.Context, string) bool { return bool(f) }

// errStore fails every call with a plain error, as a bug in the service
// would, so the handlers answer 500.
type errStore struct{}

var errBoom = errors.New("boom")

func (errStore) List(context.Context) ([]item.Item, error)      { return nil, errBoom }
func (errStore) Get(context.Context, string) (item.Item, error) { return item.Item{}, errBoom }
func (errStore) Create(context.Context, item.Item) (item.Item, error) {
	return item.Item{}, errBoom
}
func (errStore) Update(context.Context, item.Item) (item.Item, error) {
	return item.Item{}, errBoom
}
func (errStore) Delete(context.Context, string) error { return errBoom }

// seededID is the ID of the one item the "ok" environments start with.
const seededPlaceholder = "{seeded}"

// handlerFor builds the real service handler for an environment:
//
//	ok       an in-memory store holding one item; flags off
//	flagOn   the same, with the items-list-total flag on
//	internal a store that fails with a plain error (a bug here: 500)
//	badgw    an upstream that refuses connections (502)
//	timeout  an upstream that never answers (504)
//
// It returns the handler and the seeded item's ID.
func handlerFor(t *testing.T, env string) (http.Handler, string) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	switch env {
	case "ok", "flagOn":
		store := itemtest.NewMemStore()
		seeded, err := store.Create(t.Context(), item.Item{Name: "seeded", Description: "first"})
		if err != nil {
			t.Fatal(err)
		}
		return newHandler(logger, headerguard.DefaultPolicy(), testAuth(t), noLimits, store, item.WithFlags(fixedFlags(env == "flagOn"))), seeded.ID
	case "ratelimited":
		// The test client (its key) has spent its only token, and gets
		// another in about 11 days.
		limits := limit.Policy{Rate: 1e-6, Burst: 1}
		h := newHandler(logger, headerguard.DefaultPolicy(), testAuth(t), limits, itemtest.NewMemStore())
		warm := httptest.NewRequest("GET", "/items", nil)
		warm.Header.Set("Authorization", "Bearer "+testWriteKey)
		h.ServeHTTP(httptest.NewRecorder(), warm)
		return h, ""
	case "overloaded":
		// The one request the service takes at once is stuck in the store.
		store := blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
		h := newHandler(logger, headerguard.DefaultPolicy(), testAuth(t), limit.Policy{MaxInFlight: 1}, store)
		stuck := httptest.NewRequest("GET", "/items", nil)
		stuck.Header.Set("Authorization", "Bearer "+testWriteKey)
		done := make(chan struct{})
		go func() { h.ServeHTTP(httptest.NewRecorder(), stuck); close(done) }()
		<-store.entered
		t.Cleanup(func() { close(store.release); <-done })
		return h, ""
	case "internal":
		return newHandler(logger, headerguard.DefaultPolicy(), testAuth(t), noLimits, errStore{}), ""
	case "badgw":
		return newHandler(logger, headerguard.DefaultPolicy(), testAuth(t), noLimits, httpstore.New(nettest.DeadURL(), &http.Client{Timeout: time.Second})), ""
	case "timeout":
		upstream := httptest.NewServer(http.HandlerFunc(nettest.Hang))
		t.Cleanup(upstream.Close)
		return newHandler(logger, headerguard.DefaultPolicy(), testAuth(t), noLimits, httpstore.New(upstream.URL, &http.Client{Timeout: 50 * time.Millisecond})), ""
	}
	t.Fatalf("unknown environment %q", env)
	return nil, ""
}

// scenario is one request, with the status the spec says it gets.
type scenario struct {
	name         string
	env          string
	method, path string // path may contain seededPlaceholder
	op           string // the spec operation it exercises, e.g. "GET /items/{id}"
	body         string
	want         int
	check        func(t *testing.T, body []byte)

	// header is added to the request; a header with nil values is left out.
	// A request gets the test write key, and with a body, Content-Type:
	// application/json, unless header says otherwise.
	header http.Header

	// exempt says why a body the schema calls valid is refused anyway, for a
	// rule the schema can't express. Empty for every other scenario.
	exempt string
}

// scenarios covers every documented response of every operation. The failure
// statuses are generated from the spec's own list of item operations.
func scenarios(t *testing.T, s *spec) []scenario {
	t.Helper()
	input := s.lookupSchema(t, "ItemInput")
	maxName := int(input["properties"].(map[string]any)["name"].(map[string]any)["maxLength"].(float64))
	name := func(n int) string { return strings.Repeat("a", n) }
	okBody := `{"name":"widget","description":"d"}`

	list := []scenario{
		{name: "healthz", env: "ok", method: "GET", path: "/healthz", op: "GET /healthz", want: 200},

		{name: "list", env: "ok", method: "GET", path: "/items", op: "GET /items", want: 200,
			check: func(t *testing.T, body []byte) {
				var got map[string]any
				json.Unmarshal(body, &got)
				if _, present := got["total"]; present {
					t.Errorf("total is present with the flag off, but the spec says it appears only when the flag is on: %s", body)
				}
			}},
		{name: "list with the flag on", env: "flagOn", method: "GET", path: "/items", op: "GET /items", want: 200,
			check: func(t *testing.T, body []byte) {
				var got struct {
					Items []any
					Total *int
				}
				json.Unmarshal(body, &got)
				if got.Total == nil || *got.Total != len(got.Items) {
					t.Errorf("total = %v with %d items, want the count: %s", got.Total, len(got.Items), body)
				}
			}},

		{name: "create", env: "ok", method: "POST", path: "/items", op: "POST /items", body: okBody, want: 201},
		{name: "create at the name length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"` + name(maxName) + `"}`, want: 201},
		{name: "create over the name length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"` + name(maxName+1) + `"}`, want: 422},
		{name: "create with a blank name", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"   "}`, want: 422},
		{name: "create without a name", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"description":"x"}`, want: 422},
		{name: "create with malformed JSON", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":`, want: 400},
		{name: "create with an empty body", env: "ok", method: "POST", path: "/items", op: "POST /items", body: ``, want: 400},
		{name: "create with an unknown field", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"x","colour":"red"}`, want: 400},
		{name: "create with the wrong type", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":1}`, want: 400},
		{name: "create over the body limit", env: "ok", method: "POST", path: "/items", op: "POST /items",
			body: `{"name":"x","description":"` + strings.Repeat("a", httpx.MaxBodyBytes) + `"}`, want: 400,
			exempt: "the 1 MiB body limit is stated in the description; JSON Schema can't express it"},

		{name: "get", env: "ok", method: "GET", path: "/items/" + seededPlaceholder, op: "GET /items/{id}", want: 200},
		{name: "get unknown", env: "ok", method: "GET", path: "/items/nope", op: "GET /items/{id}", want: 404},

		{name: "update", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{"name":"renamed"}`, want: 200},
		{name: "update with malformed JSON", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{`, want: 400},
		{name: "update unknown", env: "ok", method: "PUT", path: "/items/nope", op: "PUT /items/{id}", body: okBody, want: 404},
		{name: "update with a blank name", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{"name":""}`, want: 422},

		{name: "delete", env: "ok", method: "DELETE", path: "/items/" + seededPlaceholder, op: "DELETE /items/{id}", want: 204},
		{name: "delete unknown", env: "ok", method: "DELETE", path: "/items/nope", op: "DELETE /items/{id}", want: 404},
	}

	// The header rules (internal/headerguard), for every operation: too many
	// headers (431), a duplicated single-use header (400), and for a body sent
	// as anything but JSON (415).
	tooMany := http.Header{}
	for i := range 101 {
		tooMany.Set(fmt.Sprintf("X-Extra-%d", i), "v")
	}
	for _, o := range s.operations(t) {
		path := strings.ReplaceAll(o.Path, "{id}", "x")
		method := strings.ToUpper(o.Method)
		body := ""
		if _, has := o.Op["requestBody"]; has {
			body = okBody
		}
		list = append(list,
			scenario{
				name: o.Key() + " with too many headers", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: tooMany, want: http.StatusRequestHeaderFieldsTooLarge,
			},
			scenario{
				name: o.Key() + " with two correlation IDs", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: http.Header{"Client-Correlation-Id": {"a", "b"}}, want: http.StatusBadRequest,
				exempt: "refused for its headers, not its body",
			},
		)
		if body != "" {
			list = append(list, scenario{
				name: o.Key() + " with a text/plain body", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: http.Header{"Content-Type": {"text/plain"}}, want: http.StatusUnsupportedMediaType,
			})
		}

		// Authentication (internal/apikey): every operation the spec says
		// needs a key refuses a request without one, and a read-only key's
		// writes.
		if sec, ok := o.Op["security"].([]any); ok && len(sec) == 0 {
			continue // exempt, like the healthcheck
		}
		list = append(list, scenario{
			name: o.Key() + " without an API key", env: "ok", method: method, path: path, op: o.Key(),
			body: body, header: http.Header{"Authorization": nil}, want: http.StatusUnauthorized,
			exempt: "refused for its key, not its body",
		})
		if method != http.MethodGet {
			list = append(list, scenario{
				name: o.Key() + " with a read-only key", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: http.Header{"Authorization": {"Bearer " + testReadKey}}, want: http.StatusForbidden,
				exempt: "refused for its key, not its body",
			})
		}
	}

	// The limits (internal/limit), for every operation but the healthcheck,
	// which is exempt: over the client's rate (429), and with the service
	// full (503).
	for _, o := range s.operations(t) {
		if o.Path == "/healthz" {
			continue
		}
		path := strings.ReplaceAll(o.Path, "{id}", "x")
		body := ""
		if _, has := o.Op["requestBody"]; has {
			body = okBody
		}
		for env, want := range map[string]int{"ratelimited": http.StatusTooManyRequests, "overloaded": http.StatusServiceUnavailable} {
			list = append(list, scenario{
				name: o.Key() + " " + env, env: env, method: strings.ToUpper(o.Method), path: path, op: o.Key(),
				body: body, want: want, exempt: "refused for its rate, not its body",
			})
		}
	}

	// The three ways the service itself or its upstream can fail, for every
	// operation that reaches the store.
	failures := map[string]int{"internal": 500, "badgw": 502, "timeout": 504}
	for _, o := range s.operations(t) {
		if o.Path == "/healthz" {
			continue
		}
		path := strings.ReplaceAll(o.Path, "{id}", "x")
		body := ""
		if _, has := o.Op["requestBody"]; has {
			body = okBody
		}
		for env, want := range failures {
			list = append(list, scenario{
				name: o.Key() + " fails with " + strconv.Itoa(want), env: env,
				method: strings.ToUpper(o.Method), path: path, op: o.Key(), body: body, want: want,
			})
		}
	}
	return list
}

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

// TestOpenAPIItemSchemaMatchesStruct fails if the Item schema and item.Item
// disagree about which fields exist and which are always present, and checks
// the input limits the spec states against the constants the code enforces.
func TestOpenAPIItemSchemaMatchesStruct(t *testing.T) {
	s := loadSpec(t)

	schema := s.lookupSchema(t, "Item")
	props := schema["properties"].(map[string]any)
	required := map[string]bool{}
	for _, r := range schema["required"].([]any) {
		required[r.(string)] = true
	}
	typ := reflect.TypeOf(item.Item{})
	fields := map[string]bool{}
	for i := range typ.NumField() {
		name, opts, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		fields[name] = true
		if _, ok := props[name]; !ok {
			t.Errorf("item.Item has JSON field %q, which the Item schema doesn't describe", name)
		}
		if always := !strings.Contains(opts, "omitempty"); always != required[name] {
			t.Errorf("field %q: always present in Go = %v, required in the spec = %v", name, always, required[name])
		}
	}
	for name := range props {
		if !fields[name] {
			t.Errorf("the Item schema describes %q, which item.Item doesn't have", name)
		}
	}

	nameSchema := s.lookupSchema(t, "ItemInput")["properties"].(map[string]any)["name"].(map[string]any)
	if got := int(nameSchema["maxLength"].(float64)); got != item.MaxNameLen {
		t.Errorf("spec says names may be %d characters; the code allows %d (item.MaxNameLen)", got, item.MaxNameLen)
	}
	if desc, _ := s.lookupSchema(t, "ItemInput")["description"].(string); !strings.Contains(desc, strconv.Itoa(httpx.MaxBodyBytes>>20)+" MiB") {
		t.Errorf("ItemInput's description should state the %d MiB body limit (httpx.MaxBodyBytes)", httpx.MaxBodyBytes>>20)
	}
}

// TestOpenAPICorrelationIDMatchesBehavior checks the Client-Correlation-Id
// header schema against what the service accepts: an ID that fits the spec's
// pattern is echoed back, and one that doesn't is replaced by a generated one
// that does.
func TestOpenAPICorrelationIDMatchesBehavior(t *testing.T) {
	s := loadSpec(t)
	param, ok := s.lookup("#/components/parameters/CorrelationID")
	if !ok {
		t.Fatal("spec has no CorrelationID parameter")
	}
	if param["name"] != httpx.RequestIDHeader {
		t.Errorf("spec names the header %q; the code uses %q", param["name"], httpx.RequestIDHeader)
	}
	pattern := regexp.MustCompile(param["schema"].(map[string]any)["pattern"].(string))

	handler, _ := handlerFor(t, "ok")
	for _, id := range []string{
		"abc", "3f2504e0-4f89-11d3-9a0c-0305e82c3301", "svc-a:req_42.1",
		strings.Repeat("a", 128), strings.Repeat("a", 129),
		"has space", `x"y`, "a\nb", "a/b", "é", "",
	} {
		t.Run(truncate(strconv.Quote(id)), func(t *testing.T) {
			req := httptest.NewRequest("GET", "/healthz", nil)
			req.Header.Set(httpx.RequestIDHeader, id)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			got := rec.Header().Get(httpx.RequestIDHeader)
			if pattern.MatchString(id) {
				if got != id {
					t.Errorf("the spec accepts %q, but the service answered with %q instead of echoing it", id, got)
				}
				return
			}
			if got == id || !pattern.MatchString(got) {
				t.Errorf("the spec rejects %q, so the service should replace it with a valid generated ID; got %q", id, got)
			}
		})
	}
}

// TestOpenAPIItemIDMatchesBehavior fails if the spec's rule for an item ID
// and the code's (item.ValidID) disagree. An ID the spec rejects must get a
// 404 without the store being asked, so it never reaches the upstream.
func TestOpenAPIItemIDMatchesBehavior(t *testing.T) {
	s := loadSpec(t)
	param, ok := s.lookup("#/components/parameters/ItemID")
	if !ok {
		t.Fatal("spec has no ItemID parameter")
	}
	schema := param["schema"].(map[string]any)
	pattern := regexp.MustCompile(schema["pattern"].(string))
	if int(schema["maxLength"].(float64)) != item.MaxIDLen {
		t.Errorf("spec maxLength = %v, item.MaxIDLen = %d", schema["maxLength"], item.MaxIDLen)
	}

	asked := false
	handler := newHandler(slog.New(slog.DiscardHandler), headerguard.DefaultPolicy(), noAuth, noLimits, spyStore{asked: &asked})
	for _, id := range []string{
		"widget", "new-item", "x_1", strings.Repeat("a", 128), strings.Repeat("a", 129),
		".", "..", "%2E%2E", "..%2F..%2Fadmin", "a%2Fb", "a%20b", "x%3Fq", "%C3%A9",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+truncate(id), func(t *testing.T) {
				asked = false
				req := httptest.NewRequest(method, "/items/"+id, strings.NewReader(`{"name":"x"}`))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				decoded, err := url.PathUnescape(id)
				if err != nil {
					t.Fatal(err)
				}
				// A literal "." or ".." never reaches the handlers: the router
				// redirects to the cleaned path first, which is as safe.
				want := http.StatusNotFound
				if id == "." || id == ".." {
					want = http.StatusTemporaryRedirect
				}
				switch {
				case pattern.MatchString(decoded) && !asked:
					t.Errorf("the spec accepts %q, but the store wasn't asked (status %d)", decoded, rec.Code)
				case !pattern.MatchString(decoded) && (asked || rec.Code != want):
					t.Errorf("the spec rejects %q: want %d without asking the store; got %d, store asked: %v", decoded, want, rec.Code, asked)
				}
			})
		}
	}
}

// spyStore notes that it was asked anything, and has no items.
type spyStore struct{ asked *bool }

func (s spyStore) List(context.Context) ([]item.Item, error) { *s.asked = true; return nil, nil }
func (s spyStore) Get(context.Context, string) (item.Item, error) {
	*s.asked = true
	return item.Item{}, item.ErrNotFound
}
func (s spyStore) Create(context.Context, item.Item) (item.Item, error) {
	*s.asked = true
	return item.Item{}, nil
}
func (s spyStore) Update(context.Context, item.Item) (item.Item, error) {
	*s.asked = true
	return item.Item{}, item.ErrNotFound
}
func (s spyStore) Delete(context.Context, string) error { *s.asked = true; return item.ErrNotFound }

// blockingStore holds every call until release is closed, after saying on
// entered that one has arrived.
type blockingStore struct{ entered, release chan struct{} }

func (s blockingStore) wait() { s.entered <- struct{}{}; <-s.release }
func (s blockingStore) List(context.Context) ([]item.Item, error) {
	s.wait()
	return nil, nil
}
func (s blockingStore) Get(context.Context, string) (item.Item, error) {
	s.wait()
	return item.Item{}, item.ErrNotFound
}
func (s blockingStore) Create(context.Context, item.Item) (item.Item, error) {
	s.wait()
	return item.Item{}, nil
}
func (s blockingStore) Update(context.Context, item.Item) (item.Item, error) {
	s.wait()
	return item.Item{}, nil
}
func (s blockingStore) Delete(context.Context, string) error { s.wait(); return nil }
