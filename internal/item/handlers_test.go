package item_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
)

func TestHandlers(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string // "{id}" is replaced with the seeded item's ID
		body       string
		wantStatus int
		check      func(t *testing.T, rec *httptest.ResponseRecorder, store item.Store, seeded item.Item)
	}{
		{
			name: "list", method: http.MethodGet, path: "/items", wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, _ item.Store, seeded item.Item) {
				var got struct{ Items []item.Response }
				decodeBody(t, rec, &got)
				if len(got.Items) != 1 || got.Items[0].ID != seeded.ID.String() {
					t.Errorf("items = %+v, want [%s]", got.Items, seeded.ID)
				}
			},
		},
		{
			name: "get", method: http.MethodGet, path: "/items/{id}", wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, _ item.Store, seeded item.Item) {
				var got item.Response
				decodeBody(t, rec, &got)
				if !sameResponse(got, item.NewResponse(seeded)) {
					t.Errorf("item = %+v, want %+v", got, seeded)
				}
			},
		},
		{name: "get missing", method: http.MethodGet, path: "/items/nope", wantStatus: http.StatusNotFound},
		{
			name: "create", method: http.MethodPost, path: "/items",
			body:       `{"name":"  new thing  ","description":"desc"}`,
			wantStatus: http.StatusCreated,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, store item.Store, _ item.Item) {
				var got item.Response
				decodeBody(t, rec, &got)
				if got.ID == "" || got.Name != "new thing" || got.Description != "desc" {
					t.Errorf("item = %+v, want trimmed name and description", got)
				}
				if loc := rec.Header().Get("Location"); loc != "/items/"+got.ID {
					t.Errorf("Location = %q, want /items/%s", loc, got.ID)
				}
				if _, err := store.Get(context.Background(), itemtest.MustID(got.ID)); err != nil {
					t.Errorf("created item not in store: %v", err)
				}
			},
		},
		{name: "create missing name", method: http.MethodPost, path: "/items", body: `{"description":"x"}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "create blank name", method: http.MethodPost, path: "/items", body: `{"name":"   "}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "create name too long", method: http.MethodPost, path: "/items", body: `{"name":"` + strings.Repeat("a", item.MaxNameLen+1) + `"}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "create description at the limit", method: http.MethodPost, path: "/items", body: `{"name":"x","description":"` + strings.Repeat("é", item.MaxDescriptionLen) + `"}`, wantStatus: http.StatusCreated},
		{name: "create description too long", method: http.MethodPost, path: "/items", body: `{"name":"x","description":"` + strings.Repeat("a", item.MaxDescriptionLen+1) + `"}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "create empty body", method: http.MethodPost, path: "/items", body: ``, wantStatus: http.StatusBadRequest},
		{name: "create malformed JSON", method: http.MethodPost, path: "/items", body: `{"name":`, wantStatus: http.StatusBadRequest},
		{name: "create unknown field", method: http.MethodPost, path: "/items", body: `{"name":"x","colour":"red"}`, wantStatus: http.StatusBadRequest},
		{name: "create body too large", method: http.MethodPost, path: "/items", body: `{"name":"x","description":"` + strings.Repeat("a", httpx.MaxBodyBytes) + `"}`, wantStatus: http.StatusBadRequest},
		{
			name: "update", method: http.MethodPut, path: "/items/{id}",
			body:       `{"name":"renamed"}`,
			wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, _ item.Store, seeded item.Item) {
				var got item.Response
				decodeBody(t, rec, &got)
				if got.ID != seeded.ID.String() || got.Name != "renamed" || !got.CreatedAt.Equal(seeded.CreatedAt) {
					t.Errorf("item = %+v, want renamed seed with original CreatedAt", got)
				}
			},
		},
		{name: "update missing", method: http.MethodPut, path: "/items/nope", body: `{"name":"x"}`, wantStatus: http.StatusNotFound},
		{name: "update invalid", method: http.MethodPut, path: "/items/{id}", body: `{"name":""}`, wantStatus: http.StatusUnprocessableEntity},
		{
			name: "delete", method: http.MethodDelete, path: "/items/{id}", wantStatus: http.StatusNoContent,
			check: func(t *testing.T, _ *httptest.ResponseRecorder, store item.Store, seeded item.Item) {
				if _, err := store.Get(context.Background(), seeded.ID); !errors.Is(err, item.ErrNotFound) {
					t.Errorf("item still in store after delete (err = %v)", err)
				}
			},
		},
		{name: "delete missing", method: http.MethodDelete, path: "/items/nope", wantStatus: http.StatusNotFound},
		{name: "wrong method", method: http.MethodPatch, path: "/items/{id}", wantStatus: http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := itemtest.NewMemStore()
			seeded, err := store.Create(t.Context(), itemtest.MustDraft("seeded", ""))
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			mux := http.NewServeMux()
			item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), store)

			path := strings.ReplaceAll(tt.path, "{id}", seeded.ID.String())
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tt.method, path, strings.NewReader(tt.body)))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.check != nil {
				tt.check(t, rec, store, seeded)
			}
		})
	}
}

// failingStore returns errStoreDown from every method, to check that
// unexpected store errors become 500s without leaking details.
type failingStore struct{}

var errStoreDown = errors.New("store is down: secret detail")

func (failingStore) List(context.Context, item.Page) (item.Listing, error) {
	return item.Listing{}, errStoreDown
}
func (failingStore) Get(context.Context, item.ID) (item.Item, error) {
	return item.Item{}, errStoreDown
}
func (failingStore) Create(context.Context, item.Draft) (item.Item, error) {
	return item.Item{}, errStoreDown
}
func (failingStore) Update(context.Context, item.ID, item.Draft) (item.Item, error) {
	return item.Item{}, errStoreDown
}
func (failingStore) Delete(context.Context, item.ID) error { return errStoreDown }

func TestHandlersStoreError(t *testing.T) {
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), failingStore{})

	tests := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/items", ""},
		{http.MethodGet, "/items/x", ""},
		{http.MethodPost, "/items", `{"name":"x"}`},
		{http.MethodPut, "/items/x", `{"name":"x"}`},
		{http.MethodDelete, "/items/x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "secret") {
				t.Errorf("response leaks store error: %s", rec.Body)
			}
		})
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// fakeFlags is a Flags that returns fixed values by key (false if absent).
type fakeFlags map[string]bool

func (f fakeFlags) Bool(_ context.Context, key string) bool { return f[key] }

func TestListTotalFlag(t *testing.T) {
	tests := []struct {
		name      string
		opts      []item.Option
		wantTotal bool
	}{
		{name: "no flags configured"},
		{name: "flag off", opts: []item.Option{item.WithFlags(fakeFlags{item.FlagListTotal: false})}},
		{name: "flag absent", opts: []item.Option{item.WithFlags(fakeFlags{})}},
		{name: "flag on", opts: []item.Option{item.WithFlags(fakeFlags{item.FlagListTotal: true})}, wantTotal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := itemtest.NewMemStore()
			for _, name := range []string{"a", "b"} {
				if _, err := store.Create(t.Context(), itemtest.MustDraft(name, "")); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			mux := http.NewServeMux()
			item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), store, tt.opts...)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))

			var got struct {
				Items []item.Response
				Total *int
			}
			decodeBody(t, rec, &got)
			if len(got.Items) != 2 {
				t.Errorf("items = %d, want 2", len(got.Items))
			}
			if tt.wantTotal {
				if got.Total == nil || *got.Total != 2 {
					t.Errorf("total = %v, want 2", got.Total)
				}
			} else if got.Total != nil {
				t.Errorf("total = %d, want it absent", *got.Total)
			}
		})
	}
}

// TestPlainStoreErrorStaysA500 checks that only upstream failures are 5xx
// other than 500: an unexpected error from anywhere else still is one.
func TestPlainStoreErrorStaysA500(t *testing.T) {
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), failingStore{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func sameResponse(a, b item.Response) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Description == b.Description &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

// TestStoreFailureStatuses checks the handlers, not the store, decide what a
// store's failure means to the client: a timeout is a 504, a failure to give a
// usable answer a 502, and any other error a 500.
func TestStoreFailureStatuses(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want int
	}{
		{fmt.Errorf("upstream GET /items: %w", item.ErrStoreTimeout), http.StatusGatewayTimeout},
		{fmt.Errorf("upstream GET /items: %w", item.ErrStoreUnavailable), http.StatusBadGateway},
		{errStoreDown, http.StatusInternalServerError},
	} {
		mux := http.NewServeMux()
		item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), erroringStore{err: tt.err})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
		if rec.Code != tt.want {
			t.Errorf("store error %q: status %d, want %d", tt.err, rec.Code, tt.want)
		}
	}
}

// erroringStore lists items with err.
type erroringStore struct {
	failingStore
	err error
}

func (s erroringStore) List(context.Context, item.Page) (item.Listing, error) {
	return item.Listing{}, s.err
}

// TestETags checks the conditional requests: an item's ETag on reads and
// writes, If-None-Match answered 304, and a write with an If-Match the item
// no longer matches refused with 412, leaving the item as it was.
func TestETags(t *testing.T) {
	store := itemtest.NewMemStore()
	seeded, err := store.Create(t.Context(), itemtest.MustDraft("seeded", ""))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), store)
	path := "/items/" + seeded.ID.String()
	do := func(method, body string, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	got := do(http.MethodGet, "")
	tag := got.Header().Get("ETag")
	if got.Code != 200 || !strings.HasPrefix(tag, `"`) || tag != item.ETag(seeded) {
		t.Fatalf("GET: %d, ETag %q; want 200 and the item's tag", got.Code, tag)
	}
	if rec := do(http.MethodGet, "", "If-None-Match", tag); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("GET with its own tag: %d %q, want an empty 304", rec.Code, rec.Body)
	}
	if rec := do(http.MethodGet, "", "If-None-Match", "W/"+tag); rec.Code != http.StatusNotModified {
		t.Errorf("GET with the weak form of its tag: %d, want 304 (If-None-Match compares weakly)", rec.Code)
	}

	// A write with the current tag goes through, and the item has a new one.
	put := do(http.MethodPut, `{"name":"renamed"}`, "If-Match", tag)
	newTag := put.Header().Get("ETag")
	if put.Code != 200 || newTag == "" || newTag == tag {
		t.Fatalf("PUT with the current tag: %d, ETag %q; want 200 and a new tag", put.Code, newTag)
	}
	// The old tag is stale now: a write with it is refused, and changes nothing.
	stale := do(http.MethodPut, `{"name":"lost update"}`, "If-Match", tag)
	if stale.Code != http.StatusPreconditionFailed || !strings.Contains(stale.Body.String(), `"code":"precondition_failed"`) {
		t.Errorf("PUT with a stale tag: %d %s, want 412 precondition_failed", stale.Code, stale.Body)
	}
	if rec := do(http.MethodDelete, "", "If-Match", tag); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("DELETE with a stale tag: %d, want 412", rec.Code)
	}
	if rec := do(http.MethodPut, `{"name":"x"}`, "If-Match", "W/"+newTag); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("PUT with a weak tag: %d, want 412 (If-Match compares strongly)", rec.Code)
	}
	if it, _ := store.Get(t.Context(), seeded.ID); it.Name.String() != "renamed" {
		t.Errorf("after refused writes the name is %q, want renamed", it.Name)
	}
	if rec := do(http.MethodDelete, "", "If-Match", "*"); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE with If-Match *: %d, want 204", rec.Code)
	}
	if rec := do(http.MethodPut, `{"name":"x"}`, "If-Match", "*"); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("PUT with If-Match * on a deleted item: %d, want 412", rec.Code)
	}
}

// TestETagChangesWithContent checks two versions of an item with the same
// timestamp, a change within one tick of the clock, still have different
// tags.
func TestETagChangesWithContent(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := item.Restore("w", "Widget", "", at, at)
	b := item.Restore("w", "Widget 2", "", at, at)
	if item.ETag(a) == item.ETag(b) {
		t.Error("two different names, one timestamp: the same ETag")
	}
	if item.ETag(a) != item.ETag(item.Restore("w", "Widget", "", at, at)) {
		t.Error("the same item has two ETags")
	}
}
