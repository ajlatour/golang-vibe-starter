package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestItemHandlers(t *testing.T) {
	seeded := Item{
		ID:        "seed1",
		Name:      "seeded",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		check      func(t *testing.T, rec *httptest.ResponseRecorder, store *memStore)
	}{
		{
			name: "list", method: http.MethodGet, path: "/items", wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, _ *memStore) {
				var got struct{ Items []Item }
				decodeBody(t, rec, &got)
				if len(got.Items) != 1 || got.Items[0].ID != seeded.ID {
					t.Errorf("items = %+v, want [%s]", got.Items, seeded.ID)
				}
			},
		},
		{
			name: "get", method: http.MethodGet, path: "/items/seed1", wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, _ *memStore) {
				var got Item
				decodeBody(t, rec, &got)
				if got != seeded {
					t.Errorf("item = %+v, want %+v", got, seeded)
				}
			},
		},
		{name: "get missing", method: http.MethodGet, path: "/items/nope", wantStatus: http.StatusNotFound},
		{
			name: "create", method: http.MethodPost, path: "/items",
			body:       `{"name":"  new thing  ","description":"desc"}`,
			wantStatus: http.StatusCreated,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, store *memStore) {
				var got Item
				decodeBody(t, rec, &got)
				if got.ID == "" || got.Name != "new thing" || got.Description != "desc" {
					t.Errorf("item = %+v, want trimmed name and description", got)
				}
				if loc := rec.Header().Get("Location"); loc != "/items/"+got.ID {
					t.Errorf("Location = %q, want /items/%s", loc, got.ID)
				}
				if _, err := store.Get(context.Background(), got.ID); err != nil {
					t.Errorf("created item not in store: %v", err)
				}
			},
		},
		{name: "create missing name", method: http.MethodPost, path: "/items", body: `{"description":"x"}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "create blank name", method: http.MethodPost, path: "/items", body: `{"name":"   "}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "create name too long", method: http.MethodPost, path: "/items", body: `{"name":"` + strings.Repeat("a", maxItemNameLen+1) + `"}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "create empty body", method: http.MethodPost, path: "/items", body: ``, wantStatus: http.StatusBadRequest},
		{name: "create malformed JSON", method: http.MethodPost, path: "/items", body: `{"name":`, wantStatus: http.StatusBadRequest},
		{name: "create unknown field", method: http.MethodPost, path: "/items", body: `{"name":"x","colour":"red"}`, wantStatus: http.StatusBadRequest},
		{name: "create trailing data", method: http.MethodPost, path: "/items", body: `{"name":"x"}{"name":"y"}`, wantStatus: http.StatusBadRequest},
		{name: "create body too large", method: http.MethodPost, path: "/items", body: `{"name":"x","description":"` + strings.Repeat("a", maxBodyBytes) + `"}`, wantStatus: http.StatusBadRequest},
		{
			name: "update", method: http.MethodPut, path: "/items/seed1",
			body:       `{"name":"renamed"}`,
			wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, _ *memStore) {
				var got Item
				decodeBody(t, rec, &got)
				if got.ID != seeded.ID || got.Name != "renamed" || !got.CreatedAt.Equal(seeded.CreatedAt) {
					t.Errorf("item = %+v, want renamed seed with original CreatedAt", got)
				}
			},
		},
		{name: "update missing", method: http.MethodPut, path: "/items/nope", body: `{"name":"x"}`, wantStatus: http.StatusNotFound},
		{name: "update invalid", method: http.MethodPut, path: "/items/seed1", body: `{"name":""}`, wantStatus: http.StatusUnprocessableEntity},
		{
			name: "delete", method: http.MethodDelete, path: "/items/seed1", wantStatus: http.StatusNoContent,
			check: func(t *testing.T, _ *httptest.ResponseRecorder, store *memStore) {
				if _, err := store.Get(context.Background(), seeded.ID); !errors.Is(err, ErrNotFound) {
					t.Errorf("item still in store after delete (err = %v)", err)
				}
			},
		},
		{name: "delete missing", method: http.MethodDelete, path: "/items/nope", wantStatus: http.StatusNotFound},
		{name: "wrong method", method: http.MethodPatch, path: "/items/seed1", wantStatus: http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMemStore()
			store.items[seeded.ID] = seeded
			srv := newServer(slog.New(slog.DiscardHandler), store)

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.check != nil {
				tt.check(t, rec, store)
			}
		})
	}
}

// failingStore returns errStoreDown from every method, to check that
// unexpected store errors become 500s without leaking details.
type failingStore struct{}

var errStoreDown = errors.New("store is down: secret detail")

func (failingStore) List(context.Context) ([]Item, error)       { return nil, errStoreDown }
func (failingStore) Get(context.Context, string) (Item, error)  { return Item{}, errStoreDown }
func (failingStore) Create(context.Context, Item) (Item, error) { return Item{}, errStoreDown }
func (failingStore) Update(context.Context, Item) (Item, error) { return Item{}, errStoreDown }
func (failingStore) Delete(context.Context, string) error       { return errStoreDown }

func TestItemHandlersStoreError(t *testing.T) {
	srv := newServer(slog.New(slog.DiscardHandler), failingStore{})

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
			srv.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
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
