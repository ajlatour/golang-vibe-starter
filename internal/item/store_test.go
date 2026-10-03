package item_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

func TestMemStore(t *testing.T) {
	testStore(t, func(t *testing.T) item.Store { return item.NewMemStore() })
}

// TestHTTPStore runs the contract against an HTTPStore talking to an
// in-process upstream that serves the item routes from a MemStore, the same
// shape as the mock service, without needing a container.
func TestHTTPStore(t *testing.T) {
	testStore(t, func(t *testing.T) item.Store {
		return item.NewHTTPStore(newUpstream(t).URL, &http.Client{})
	})
}

// newUpstream starts an httptest server serving the item routes from a fresh
// MemStore.
func newUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), item.NewMemStore())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// testStore checks the behaviour every item.Store must have. Any new
// implementation (e.g. a datastore-backed one) should run it too.
func testStore(t *testing.T, newStore func(t *testing.T) item.Store) {
	t.Run("lifecycle", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)

		created, err := s.Create(ctx, item.Item{Name: "widget", Description: "a widget"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.ID == "" || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
			t.Fatalf("Create returned %+v, want ID and equal non-zero timestamps", created)
		}
		if created.Name != "widget" || created.Description != "a widget" {
			t.Errorf("Create returned %+v, want input fields preserved", created)
		}

		got, err := s.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !sameItem(got, created) {
			t.Errorf("Get = %+v, want %+v", got, created)
		}

		updated, err := s.Update(ctx, item.Item{ID: created.ID, Name: "gadget"})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.Name != "gadget" || updated.Description != "" {
			t.Errorf("Update did not replace fields: %+v", updated)
		}
		if !updated.CreatedAt.Equal(created.CreatedAt) || updated.UpdatedAt.Before(created.UpdatedAt) {
			t.Errorf("Update timestamps wrong: created %+v, updated %+v", created, updated)
		}

		list, err := s.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 1 || !sameItem(list[0], updated) {
			t.Errorf("List = %+v, want [%+v]", list, updated)
		}

		if err := s.Delete(ctx, created.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := s.Get(ctx, created.ID); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Get after Delete error = %v, want ErrNotFound", err)
		}
	})

	t.Run("list order", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)
		var want []string
		for _, name := range []string{"a", "b", "c"} {
			it, err := s.Create(ctx, item.Item{Name: name})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			want = append(want, it.ID)
		}
		list, err := s.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != len(want) {
			t.Fatalf("len(List) = %d, want %d", len(list), len(want))
		}
		// Items created in the same clock tick tie on CreatedAt, so only
		// check that order is non-decreasing by creation time.
		for i := 1; i < len(list); i++ {
			if list[i].CreatedAt.Before(list[i-1].CreatedAt) {
				t.Errorf("List not oldest first: %+v", list)
			}
		}
	})

	t.Run("missing ID", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)
		if _, err := s.Get(ctx, "nope"); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Get error = %v, want ErrNotFound", err)
		}
		if _, err := s.Update(ctx, item.Item{ID: "nope", Name: "x"}); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Update error = %v, want ErrNotFound", err)
		}
		if err := s.Delete(ctx, "nope"); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Delete error = %v, want ErrNotFound", err)
		}
	})

	t.Run("list empty is non-nil", func(t *testing.T) {
		list, err := newStore(t).List(t.Context())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if list == nil {
			t.Error("List = nil, want empty slice so it encodes as []")
		}
	})

	t.Run("concurrent creates", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)
		const n = 50
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				if _, err := s.Create(ctx, item.Item{Name: "x"}); err != nil {
					t.Errorf("Create: %v", err)
				}
			})
		}
		wg.Wait()
		list, err := s.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != n {
			t.Errorf("len(List) = %d, want %d", len(list), n)
		}
	})
}

// sameItem compares items with time.Equal, since timestamps that went
// through JSON lose their monotonic clock reading and location pointer.
func sameItem(a, b item.Item) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Description == b.Description &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}
