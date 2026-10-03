package item_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// TestHTTPStoreUpstreamResponses checks how HTTPStore handles upstream
// responses the contract test can't produce: failures, junk, and
// misconfiguration.
func TestHTTPStoreUpstreamResponses(t *testing.T) {
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

			_, err := item.NewHTTPStore(upstream.URL, upstream.Client()).Get(t.Context(), "a")

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

func TestHTTPStoreRequests(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string // appended to the upstream URL
		id       string
		wantPath string // escaped path the upstream sees
	}{
		{name: "plain", baseURL: "", id: "abc", wantPath: "/items/abc"},
		{name: "trailing slash on base", baseURL: "/", id: "abc", wantPath: "/items/abc"},
		{name: "path prefix", baseURL: "/v1/", id: "abc", wantPath: "/v1/items/abc"},
		{name: "ID is escaped", baseURL: "", id: "a/b c", wantPath: "/items/a%2Fb%20c"},
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

			store := item.NewHTTPStore(upstream.URL+tt.baseURL, upstream.Client())
			if _, err := store.Get(t.Context(), tt.id); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if gotPath != tt.wantPath {
				t.Errorf("upstream path = %q, want %q", gotPath, tt.wantPath)
			}
		})
	}
}

func TestHTTPStoreUnreachable(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	upstream.Close()

	store := item.NewHTTPStore(upstream.URL, &http.Client{Timeout: time.Second})
	if _, err := store.List(t.Context()); err == nil || errors.Is(err, item.ErrNotFound) {
		t.Errorf("List error = %v, want connection error", err)
	}
}

func TestHTTPStoreTimeout(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer upstream.Close()
	defer close(release)

	store := item.NewHTTPStore(upstream.URL, &http.Client{Timeout: 50 * time.Millisecond})
	if _, err := store.List(t.Context()); err == nil {
		t.Error("List error = nil, want timeout")
	}
}
