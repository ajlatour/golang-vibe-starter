package httpstore_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
)

// TestStoreUpstreamResponses checks how Store handles upstream
// responses the contract test can't produce: failures, junk, and
// misconfiguration.
func TestStoreUpstreamResponses(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		contentType  string
		body         string
		wantNotFound bool
		wantErr      string // substring; "" means success
	}{
		{name: "ok", status: http.StatusOK, contentType: "application/json", body: `{"id":"a","name":"x"}`},
		{name: "json 404 is not found", status: http.StatusNotFound, contentType: "application/json; charset=utf-8", body: `{"error":"item not found"}`, wantNotFound: true},
		{name: "plain 404 is a misconfigured route", status: http.StatusNotFound, contentType: "text/plain", body: "404 page not found", wantErr: "unexpected status 404"},
		{name: "500 includes upstream message", status: http.StatusInternalServerError, contentType: "application/json", body: `{"error":"db down"}`, wantErr: `"db down"`},
		{name: "502 without body", status: http.StatusBadGateway, wantErr: "unexpected status 502"},
		{name: "malformed JSON", status: http.StatusOK, contentType: "application/json", body: `{"id":`, wantErr: "decode response"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer upstream.Close()

			_, err := httpstore.New(upstream.URL, upstream.Client()).Get(t.Context(), itemtest.MustID("a"))

			switch {
			case tt.wantNotFound:
				if !errors.Is(err, item.ErrNotFound) {
					t.Errorf("error = %v, want ErrNotFound", err)
				}
			case tt.wantErr == "":
				if err != nil {
					t.Errorf("error = %v, want nil", err)
				}
			default:
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || errors.Is(err, item.ErrNotFound) {
					t.Errorf("error = %v, want non-NotFound error containing %q", err, tt.wantErr)
				}
			}
		})
	}
}

func TestStoreRequests(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string // appended to the upstream URL
		id       string
		wantPath string // escaped path the upstream sees
	}{
		{name: "plain", baseURL: "", id: "abc", wantPath: "/items/abc"},
		{name: "trailing slash on base", baseURL: "/", id: "abc", wantPath: "/items/abc"},
		{name: "path prefix", baseURL: "/v1/", id: "abc", wantPath: "/v1/items/abc"},
		{name: "every allowed character", baseURL: "", id: "aZ09-_", wantPath: "/items/aZ09-_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.EscapedPath()
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"id":"x"}`))
			}))
			defer upstream.Close()

			store := httpstore.New(upstream.URL+tt.baseURL, upstream.Client())
			if _, err := store.Get(t.Context(), itemtest.MustID(tt.id)); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if gotPath != tt.wantPath {
				t.Errorf("upstream path = %q, want %q", gotPath, tt.wantPath)
			}
		})
	}
}

// TestStoreLive checks Store against a running upstream, to catch
// drift between the WireMock stubs and what Store expects. It is skipped
// unless LIVE_UPSTREAM_URL is set, e.g.:
//
//	docker compose up -d --wait mock
//	LIVE_UPSTREAM_URL=http://localhost:8081 go test ./internal/item -run Live
//
// Assertions hold for a stateless stub server and a real backend alike: the
// only item it updates or deletes is the one it created.
func TestStoreLive(t *testing.T) {
	base := os.Getenv("LIVE_UPSTREAM_URL")
	if base == "" {
		t.Skip("LIVE_UPSTREAM_URL not set")
	}
	ctx := t.Context()
	store := httpstore.New(base, &http.Client{Timeout: 5 * time.Second})

	l, err := store.List(ctx, firstPage)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	list := l.Items
	if len(list) == 0 {
		t.Fatal("List returned no items; want seeded data")
	}
	for _, it := range list {
		if it.ID.IsZero() || it.Name.String() == "" || it.CreatedAt.IsZero() || it.UpdatedAt.IsZero() {
			t.Errorf("List item missing fields: %+v", it)
		}
	}
	got, err := store.Get(ctx, list[0].ID)
	if err != nil {
		t.Fatalf("Get(%q): %v", list[0].ID, err)
	}
	if !sameItem(got, list[0]) {
		t.Errorf("Get = %+v, want %+v (as listed)", got, list[0])
	}

	created, err := store.Create(ctx, itemtest.MustDraft("live test", `say "hi"`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID.IsZero() || created.Name.String() != "live test" || created.Description.String() != `say "hi"` || created.CreatedAt.IsZero() {
		t.Errorf("Create = %+v, want ID, timestamps and echoed fields", created)
	}
	updated, err := store.Update(ctx, created.ID, itemtest.MustDraft("live renamed", ""))
	if err != nil {
		t.Fatalf("Update(%q): %v", created.ID, err)
	}
	if updated.ID != created.ID || updated.Name.String() != "live renamed" || updated.Description.String() != "" {
		t.Errorf("Update = %+v, want ID %q with replaced fields", updated, created.ID)
	}
	if err := store.Delete(ctx, created.ID); err != nil {
		t.Errorf("Delete(%q): %v", created.ID, err)
	}

	if _, err := store.Get(ctx, itemtest.MustID("does-not-exist")); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Get unknown error = %v, want ErrNotFound", err)
	}
	if _, err := store.Update(ctx, itemtest.MustID("does-not-exist"), itemtest.MustDraft("x", "")); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Update unknown error = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, itemtest.MustID("does-not-exist")); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Delete unknown error = %v, want ErrNotFound", err)
	}
}

func TestStoreUnreachable(t *testing.T) {
	store := httpstore.New(nettest.DeadURL(), &http.Client{Timeout: time.Second})
	if _, err := store.List(t.Context(), firstPage); err == nil || errors.Is(err, item.ErrNotFound) {
		t.Errorf("List error = %v, want connection error", err)
	}
}

func TestStoreTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(nettest.Hang))
	defer upstream.Close()

	store := httpstore.New(upstream.URL, &http.Client{Timeout: 50 * time.Millisecond})
	if _, err := store.List(t.Context(), firstPage); err == nil {
		t.Error("List error = nil, want timeout")
	}
}

// TestZeroIDNeverReachesTheUpstream checks the store refuses the zero ID
// without calling the upstream. An item.ID can't hold a crafted one (".." or
// "..%2F..%2Fadmin", which an upstream might normalize or decode into another
// route): item.ParseID refuses those, as its tests and fuzz test check.
func TestZeroIDNeverReachesTheUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Errorf("the upstream was called: %s %s", r.Method, r.URL.EscapedPath())
	}))
	defer upstream.Close()
	store := httpstore.New(upstream.URL, upstream.Client())

	var zero item.ID
	if _, err := store.Get(t.Context(), zero); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Get(zero ID) error = %v, want ErrNotFound", err)
	}
	if _, err := store.Update(t.Context(), zero, itemtest.MustDraft("x", "")); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Update(zero ID) error = %v, want ErrNotFound", err)
	}
	if err := store.Delete(t.Context(), zero); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Delete(zero ID) error = %v, want ErrNotFound", err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the upstream was called %d times", n)
	}
}

// TestUpstreamRedirectIsAFailure checks an upstream's redirect is a failed
// call (502 to the API's client), not followed to wherever it points.
func TestUpstreamRedirectIsAFailure(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect was followed: %s %s", r.Method, r.URL)
	}))
	defer elsewhere.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/latest/meta-data", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()

	store := httpstore.New(upstream.URL, &http.Client{Timeout: time.Second})
	_, err := store.Create(t.Context(), itemtest.MustDraft("x", ""))
	var uerr *httpstore.UpstreamError
	if !errors.As(err, &uerr) || uerr.Status != http.StatusTemporaryRedirect || !errors.Is(err, item.ErrStoreUnavailable) || errors.Is(err, item.ErrStoreTimeout) {
		t.Errorf("Create error = %v; want an UpstreamError for the 307 that is ErrStoreUnavailable", err)
	}
}

func TestReachable(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"ok":       func(w http.ResponseWriter, r *http.Request) {},
		"an error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
		"a redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/", http.StatusFound)
		},
	} {
		t.Run("answers with "+name, func(t *testing.T) {
			upstream := httptest.NewServer(handler)
			defer upstream.Close()
			if err := httpstore.New(upstream.URL, upstream.Client()).Reachable(t.Context()); err != nil {
				t.Errorf("Reachable() = %v; any answer means reachable", err)
			}
		})
	}
	t.Run("refuses connections", func(t *testing.T) {
		err := httpstore.New("http://user:SECRET@"+strings.TrimPrefix(nettest.DeadURL(), "http://"), &http.Client{}).Reachable(t.Context())
		if err == nil {
			t.Fatal("Reachable() = nil for a dead address")
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("the error carries the URL's password: %v", err)
		}
	})
}

// firstPage is the first page of the list, at the API's default size.
var firstPage = item.Page{Limit: item.DefaultPageSize}
