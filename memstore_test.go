package main

import (
	"errors"
	"sync"
	"testing"
)

func TestMemStore(t *testing.T) {
	testItemStore(t, func() ItemStore { return newMemStore() })
}

// testItemStore checks the behaviour every ItemStore must have. A future
// datastore-backed implementation should pass it too.
func testItemStore(t *testing.T, newStore func() ItemStore) {
	t.Run("lifecycle", func(t *testing.T) {
		ctx := t.Context()
		s := newStore()

		created, err := s.Create(ctx, Item{Name: "widget", Description: "a widget"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.ID == "" || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
			t.Fatalf("Create returned %+v, want ID and equal non-zero timestamps", created)
		}

		got, err := s.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got != created {
			t.Errorf("Get = %+v, want %+v", got, created)
		}

		updated, err := s.Update(ctx, Item{ID: created.ID, Name: "gadget"})
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
		if len(list) != 1 || list[0] != updated {
			t.Errorf("List = %+v, want [%+v]", list, updated)
		}

		if err := s.Delete(ctx, created.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := s.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get after Delete error = %v, want ErrNotFound", err)
		}
	})

	t.Run("missing ID", func(t *testing.T) {
		ctx := t.Context()
		s := newStore()
		if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get error = %v, want ErrNotFound", err)
		}
		if _, err := s.Update(ctx, Item{ID: "nope", Name: "x"}); !errors.Is(err, ErrNotFound) {
			t.Errorf("Update error = %v, want ErrNotFound", err)
		}
		if err := s.Delete(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Delete error = %v, want ErrNotFound", err)
		}
	})

	t.Run("list empty is non-nil", func(t *testing.T) {
		list, err := newStore().List(t.Context())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if list == nil {
			t.Error("List = nil, want empty slice so it encodes as []")
		}
	})

	t.Run("concurrent creates", func(t *testing.T) {
		ctx := t.Context()
		s := newStore()
		const n = 50
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				if _, err := s.Create(ctx, Item{Name: "x"}); err != nil {
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
