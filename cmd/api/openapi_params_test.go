package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

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
	typ := reflect.TypeOf(item.Response{})
	fields := map[string]bool{}
	for i := range typ.NumField() {
		name, opts, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
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
	handler := newHandler(slog.New(slog.DiscardHandler), handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: noAuth}, spyStore{asked: &asked})
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
