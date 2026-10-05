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

			_, err := httpstore.New(upstream.URL, upstream.Client()).Get(t.Context(), "a")

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
			if _, err := store.Get(t.Context(), tt.id); err != nil {
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

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("List returned no items; want seeded data")
	}
	for _, it := range list {
		if it.ID == "" || it.Name == "" || it.CreatedAt.IsZero() || it.UpdatedAt.IsZero() {
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

	created, err := store.Create(ctx, item.Item{Name: "live test", Description: `say "hi"`})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" || created.Name != "live test" || created.Description != `say "hi"` || created.CreatedAt.IsZero() {
		t.Errorf("Create = %+v, want ID, timestamps and echoed fields", created)
	}
	updated, err := store.Update(ctx, item.Item{ID: created.ID, Name: "live renamed"})
	if err != nil {
		t.Fatalf("Update(%q): %v", created.ID, err)
	}
	if updated.ID != created.ID || updated.Name != "live renamed" || updated.Description != "" {
		t.Errorf("Update = %+v, want ID %q with replaced fields", updated, created.ID)
	}
	if err := store.Delete(ctx, created.ID); err != nil {
		t.Errorf("Delete(%q): %v", created.ID, err)
	}

	if _, err := store.Get(ctx, "does-not-exist"); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Get unknown error = %v, want ErrNotFound", err)
	}
	if _, err := store.Update(ctx, item.Item{ID: "does-not-exist", Name: "x"}); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Update unknown error = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, "does-not-exist"); !errors.Is(err, item.ErrNotFound) {
		t.Errorf("Delete unknown error = %v, want ErrNotFound", err)
	}
}

func TestStoreUnreachable(t *testing.T) {
	store := httpstore.New(nettest.DeadURL(), &http.Client{Timeout: time.Second})
	if _, err := store.List(t.Context()); err == nil || errors.Is(err, item.ErrNotFound) {
		t.Errorf("List error = %v, want connection error", err)
	}
}

func TestStoreTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(nettest.Hang))
	defer upstream.Close()

	store := httpstore.New(upstream.URL, &http.Client{Timeout: 50 * time.Millisecond})
	if _, err := store.List(t.Context()); err == nil {
		t.Error("List error = nil, want timeout")
	}
}

// TestInvalidIDsNeverReachTheUpstream checks the store refuses an ID that
// can't name an item (see item.ValidID) without calling the upstream, so a
// crafted ID can't be normalized or decoded into another route there.
func TestInvalidIDsNeverReachTheUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Errorf("the upstream was called: %s %s", r.Method, r.URL.EscapedPath())
	}))
	defer upstream.Close()
	store := httpstore.New(upstream.URL, upstream.Client())

	for _, id := range []string{"", ".", "..", "../admin", "..%2F..%2Fadmin", "a/b", "a b", "x?q=1", "x#f", strings.Repeat("a", item.MaxIDLen+1)} {
		if _, err := store.Get(t.Context(), id); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Get(%q) error = %v, want ErrNotFound", id, err)
		}
		if _, err := store.Update(t.Context(), item.Item{ID: id, Name: "x"}); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Update(%q) error = %v, want ErrNotFound", id, err)
		}
		if err := store.Delete(t.Context(), id); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Delete(%q) error = %v, want ErrNotFound", id, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the upstream was called %d times", n)
	}
}
