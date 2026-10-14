package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
)

// itemsTest is what the spec tests need of /items (testResources).
var itemsTest = testResource{
	path:      "/items",
	okBody:    itemOKBody,
	stubIDs:   []string{"widget", "gadget", "gizmo", "new-item"},
	routes:    itemTestRoutes,
	guide:     itemGuideRoutes,
	scenarios: itemScenarios,
}

const itemOKBody = `{"name":"widget","description":"d"}`

// itemTestRoutes is /items on a store for env (handlerFor).
func itemTestRoutes(t *testing.T, env string, up upstream) (routes, []httpx.Check) {
	t.Helper()
	switch env {
	case "ok", "flagOn":
		store := itemtest.NewMemStore()
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		store.Seed(item.Restore(seededID, "seeded", "first", at, at))
		return itemRoutes(store, item.WithFlags(fixedFlags(env == "flagOn"))), nil
	case "paged":
		// Three items, so a page of two has a next.
		store := itemtest.NewMemStore()
		for _, name := range []string{"a", "b", "c"} {
			if _, err := store.Create(t.Context(), itemtest.MustDraft(name, "")); err != nil {
				t.Fatal(err)
			}
		}
		return itemRoutes(store), nil
	case "internal":
		return itemRoutes(itemErrStore{}), nil
	case "badgw", "timeout":
		store := httpstore.New(up.url, up.client)
		return itemRoutes(store), []httpx.Check{{Name: "upstream", Fn: store.Reachable}}
	}
	return itemRoutes(itemtest.NewMemStore()), nil
}

// itemGuideRoutes is /items on a store holding the items the WireMock stubs
// serve, for docs/curl.md's examples.
func itemGuideRoutes(*testing.T) routes {
	store := itemtest.NewMemStore()
	at := func(sec int) time.Time { return time.Date(2026, 1, 1, 0, 0, sec, 0, time.UTC) }
	store.Seed(
		item.Restore("widget", "Widget", "A standard widget.", at(0), at(0)),
		item.Restore("gadget", "Gadget", "A gadget with extra features.", at(1), at(1)),
		item.Restore("gizmo", "Gizmo", "", at(2), at(2)),
	)
	return itemRoutes(store)
}

// itemScenarios are /items' own scenarios: its successes, and the client's
// mistakes it answers.
func itemScenarios(t *testing.T, s *spec) []scenario {
	t.Helper()
	input := s.lookupSchema(t, "ItemInput")
	maxName := int(input["properties"].(map[string]any)["name"].(map[string]any)["maxLength"].(float64))
	maxDesc := int(input["properties"].(map[string]any)["description"].(map[string]any)["maxLength"].(float64))
	name := func(n int) string { return strings.Repeat("a", n) }

	list := []scenario{
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

		{name: "create", env: "ok", method: "POST", path: "/items", op: "POST /items", body: itemOKBody, want: 201},
		{name: "create at the name length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"` + name(maxName) + `"}`, want: 201},
		{name: "create over the name length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"` + name(maxName+1) + `"}`, want: 422},
		{name: "create at the description length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"x","description":"` + name(maxDesc) + `"}`, want: 201},
		{name: "create over the description length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"x","description":"` + name(maxDesc+1) + `"}`, want: 422},
		{name: "create with a blank name", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"   "}`, want: 422},
		{name: "create without a name", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"description":"x"}`, want: 422},
		{name: "create with malformed JSON", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":`, want: 400},
		{name: "create with an empty body", env: "ok", method: "POST", path: "/items", op: "POST /items", body: ``, want: 400},
		{name: "create with an unknown field", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"x","colour":"red"}`, want: 400},
		{name: "create with the wrong type", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":1}`, want: 400},
		{name: "create over the body limit", env: "ok", method: "POST", path: "/items", op: "POST /items",
			body: `{"name":"x","description":"` + strings.Repeat("a", httpx.MaxBodyBytes) + `"}`, want: 400,
			exempt: "the 1 MiB body limit is stated in the description; JSON Schema can't express it"},

		{name: "list a page with more after it", env: "paged", method: "GET", path: "/items?limit=2", op: "GET /items", want: 200},
		{name: "list with a limit out of range", env: "ok", method: "GET", path: "/items?limit=201", op: "GET /items", want: 400},
		{name: "list with a malformed cursor", env: "ok", method: "GET", path: "/items?cursor=no%2Fslashes", op: "GET /items", want: 400},
		{name: "list with a cursor it didn't give out", env: "ok", method: "GET", path: "/items?cursor=bm90LWEtY3Vyc29y", op: "GET /items", want: 400},

		{name: "get", env: "ok", method: "GET", path: "/items/" + seededPlaceholder, op: "GET /items/{id}", want: 200},
		{name: "get unknown", env: "ok", method: "GET", path: "/items/nope", op: "GET /items/{id}", want: 404},
		{name: "get, unchanged since a tag", env: "ok", method: "GET", path: "/items/" + seededPlaceholder, op: "GET /items/{id}",
			header: http.Header{"If-None-Match": {"*"}}, want: 304},

		{name: "update", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{"name":"renamed"}`, want: 200},
		{name: "update with malformed JSON", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{`, want: 400},
		{name: "update unknown", env: "ok", method: "PUT", path: "/items/nope", op: "PUT /items/{id}", body: itemOKBody, want: 404},
		{name: "update with a stale tag", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: itemOKBody,
			header: http.Header{"If-Match": {`"stale"`}}, want: 412},
		{name: "update with a blank name", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{"name":""}`, want: 422},

		{name: "delete", env: "ok", method: "DELETE", path: "/items/" + seededPlaceholder, op: "DELETE /items/{id}", want: 204},
		{name: "delete unknown", env: "ok", method: "DELETE", path: "/items/nope", op: "DELETE /items/{id}", want: 404},
		{name: "delete with a stale tag", env: "ok", method: "DELETE", path: "/items/" + seededPlaceholder, op: "DELETE /items/{id}",
			header: http.Header{"If-Match": {`"stale"`}}, want: 412},
	}
	for i := range list {
		list[i].name = "items: " + list[i].name
	}
	return list
}

// itemErrStore fails every call with a plain error, as a bug in the service
// would, so the handlers answer 500.
type itemErrStore struct{}

func (itemErrStore) List(context.Context, item.Page) (item.Listing, error) {
	return item.Listing{}, errBoom
}

func (itemErrStore) Get(context.Context, item.ID) (item.Item, error) { return item.Item{}, errBoom }

func (itemErrStore) Create(context.Context, item.Draft) (item.Item, error) {
	return item.Item{}, errBoom
}

func (itemErrStore) Update(context.Context, item.ID, item.Draft) (item.Item, error) {
	return item.Item{}, errBoom
}

func (itemErrStore) Delete(context.Context, item.ID) error { return errBoom }

// itemSpyStore notes that it was asked anything, and has no items.
type itemSpyStore struct{ asked *bool }

func (s itemSpyStore) List(context.Context, item.Page) (item.Listing, error) {
	*s.asked = true
	return item.Listing{Items: []item.Item{}}, nil
}

func (s itemSpyStore) Get(context.Context, item.ID) (item.Item, error) {
	*s.asked = true
	return item.Item{}, item.ErrNotFound
}

func (s itemSpyStore) Create(context.Context, item.Draft) (item.Item, error) {
	*s.asked = true
	return item.Item{}, nil
}

func (s itemSpyStore) Update(context.Context, item.ID, item.Draft) (item.Item, error) {
	*s.asked = true
	return item.Item{}, item.ErrNotFound
}

func (s itemSpyStore) Delete(context.Context, item.ID) error {
	*s.asked = true
	return item.ErrNotFound
}

// TestOpenAPIItemSchemaMatchesStruct fails if the Item schema and
// item.Response, the shape the API sends an item in, disagree about which fields exist and which are always present, and checks
// the input limits the spec states against the constants the code enforces.
func TestOpenAPIItemSchemaMatchesStruct(t *testing.T) {
	s := loadSpec(t)

	schema := s.lookupSchema(t, "Item")
	props := schema["properties"].(map[string]any)
	required := map[string]bool{}
	for _, r := range schema["required"].([]any) {
		required[r.(string)] = true
	}
	typ := reflect.TypeFor[item.Response]()
	fields := map[string]bool{}
	for field := range typ.Fields() {
		name, opts, _ := strings.Cut(field.Tag.Get("json"), ",")
		fields[name] = true
		if _, ok := props[name]; !ok {
			t.Errorf("item.Response has JSON field %q, which the Item schema doesn't describe", name)
		}
		if always := !strings.Contains(opts, "omitempty"); always != required[name] {
			t.Errorf("field %q: always present in Go = %v, required in the spec = %v", name, always, required[name])
		}
	}
	for name := range props {
		if !fields[name] {
			t.Errorf("the Item schema describes %q, which item.Response doesn't have", name)
		}
	}

	nameSchema := s.lookupSchema(t, "ItemInput")["properties"].(map[string]any)["name"].(map[string]any)
	if got := int(nameSchema["maxLength"].(float64)); got != item.MaxNameLen {
		t.Errorf("spec says names may be %d characters; the code allows %d (item.MaxNameLen)", got, item.MaxNameLen)
	}
	descSchema := s.lookupSchema(t, "ItemInput")["properties"].(map[string]any)["description"].(map[string]any)
	if got, _ := descSchema["maxLength"].(float64); int(got) != item.MaxDescriptionLen {
		t.Errorf("spec says descriptions may be %v characters; the code allows %d (item.MaxDescriptionLen)", descSchema["maxLength"], item.MaxDescriptionLen)
	}
	if desc, _ := s.lookupSchema(t, "ItemInput")["description"].(string); !strings.Contains(desc, strconv.Itoa(httpx.MaxBodyBytes>>20)+" MiB") {
		t.Errorf("ItemInput's description should state the %d MiB body limit (httpx.MaxBodyBytes)", httpx.MaxBodyBytes>>20)
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
	handler := newHandler(slog.New(slog.DiscardHandler), handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: noAuth}, itemRoutes(itemSpyStore{asked: &asked}))
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
