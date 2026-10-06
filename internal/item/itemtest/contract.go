package itemtest

import (
	"errors"
	"sync"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// StoreContract checks the behaviour every item.Store must have: call it from
// a test with a function that returns a fresh, empty store. MemStore and
// httpstore.Store both pass it, and any new implementation (a database-backed
// one, say) should run it too.
func StoreContract(t *testing.T, newStore func(t *testing.T) item.Store) {
	t.Run("lifecycle", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)

		created, err := s.Create(ctx, MustDraft("widget", "a widget"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.ID.IsZero() || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
			t.Fatalf("Create returned %+v, want ID and equal non-zero timestamps", created)
		}
		if created.Name.String() != "widget" || created.Description.String() != "a widget" {
			t.Errorf("Create returned %+v, want input fields preserved", created)
		}

		got, err := s.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !sameItem(got, created) {
			t.Errorf("Get = %+v, want %+v", got, created)
		}

		updated, err := s.Update(ctx, created.ID, MustDraft("gadget", ""))
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.Name.String() != "gadget" || updated.Description.String() != "" {
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
		var want []item.ID
		for _, name := range []string{"a", "b", "c"} {
			it, err := s.Create(ctx, MustDraft(name, ""))
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

	// An item.ID can't hold an invalid ID (item.ParseID's tests cover those),
	// but it can be zero, which names no item.
	t.Run("the zero ID is never found", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)
		var zero item.ID
		if _, err := s.Get(ctx, zero); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Get(zero ID) error = %v, want ErrNotFound", err)
		}
		if _, err := s.Update(ctx, zero, MustDraft("x", "")); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Update(zero ID) error = %v, want ErrNotFound", err)
		}
		if err := s.Delete(ctx, zero); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Delete(zero ID) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("missing ID", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)
		if _, err := s.Get(ctx, MustID("nope")); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Get error = %v, want ErrNotFound", err)
		}
		if _, err := s.Update(ctx, MustID("nope"), MustDraft("x", "")); !errors.Is(err, item.ErrNotFound) {
			t.Errorf("Update error = %v, want ErrNotFound", err)
		}
		if err := s.Delete(ctx, MustID("nope")); !errors.Is(err, item.ErrNotFound) {
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
				if _, err := s.Create(ctx, MustDraft("x", "")); err != nil {
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

// sameItem compares the fields a store must round-trip, timestamps with
// time.Equal, since those that went through JSON lose their monotonic clock
// reading and location pointer.
func sameItem(a, b item.Item) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Description == b.Description &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}
