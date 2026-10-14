package itemtest

import (
	"errors"
	"fmt"
	"slices"
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

		list, err := listAll(t, s)
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
		list, err := listAll(t, s)
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
		l, err := newStore(t).List(t.Context(), item.Page{Limit: item.DefaultPageSize})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if l.Items == nil || !l.Next.IsZero() {
			t.Errorf("List = %+v, want an empty, non-nil page (so it encodes as []) and no next", l)
		}
	})

	t.Run("pages", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)
		var want []item.ID
		for i := range 5 {
			it, err := s.Create(ctx, MustDraft(string(rune('a'+i)), ""))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			want = append(want, it.ID)
		}
		// Pages of 2: 2, 2, then 1 with no next.
		var got []item.ID
		var sizes []int
		p := item.Page{Limit: 2}
		for {
			l, err := s.List(ctx, p)
			if err != nil {
				t.Fatalf("List(%+v): %v", p, err)
			}
			sizes = append(sizes, len(l.Items))
			for _, it := range l.Items {
				got = append(got, it.ID)
			}
			if l.Next.IsZero() {
				break
			}
			if len(sizes) > 5 {
				t.Fatal("more pages than items: the cursor doesn't move")
			}
			p.Cursor = l.Next
		}
		if fmt.Sprint(sizes) != "[2 2 1]" {
			t.Errorf("page sizes = %v, want [2 2 1]", sizes)
		}
		if !slices.Equal(sortedIDs(got), sortedIDs(want)) || len(got) != len(want) {
			t.Errorf("pages listed %v, want each of %v once", got, want)
		}
	})

	t.Run("pages stay right while the list changes", func(t *testing.T) {
		ctx := t.Context()
		s := newStore(t)
		var ids []item.ID
		for i := range 4 {
			it, err := s.Create(ctx, MustDraft(string(rune('a'+i)), ""))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			ids = append(ids, it.ID)
		}
		first, err := s.List(ctx, item.Page{Limit: 2})
		if err != nil || first.Next.IsZero() {
			t.Fatalf("first page: %+v, %v; want 2 items and a next", first, err)
		}
		// Between pages, an item already listed is deleted, and one is added.
		if err := s.Delete(ctx, first.Items[0].ID); err != nil {
			t.Fatal(err)
		}
		added, err := s.Create(ctx, MustDraft("late", ""))
		if err != nil {
			t.Fatal(err)
		}
		var rest []item.ID
		for p := (item.Page{Limit: 2, Cursor: first.Next}); ; {
			l, err := s.List(ctx, p)
			if err != nil {
				t.Fatalf("next page: %v", err)
			}
			for _, it := range l.Items {
				rest = append(rest, it.ID)
			}
			if l.Next.IsZero() {
				break
			}
			p.Cursor = l.Next
		}
		// Nothing listed twice, nothing not yet listed skipped: the rest is
		// every item the first page didn't list, and the one added since.
		// Which items the first page holds is the store's order, not
		// necessarily the order they were made in: two made in one tick of
		// the clock may come either way round.
		var want []item.ID
		for _, id := range ids {
			if !slices.ContainsFunc(first.Items, func(it item.Item) bool { return it.ID == id }) {
				want = append(want, id)
			}
		}
		want = append(want, added.ID)
		if !slices.Equal(sortedIDs(rest), sortedIDs(want)) {
			t.Errorf("after the first page, listed %v; want %v", rest, want)
		}
	})

	t.Run("a cursor it didn't make", func(t *testing.T) {
		s := newStore(t)
		c, err := item.ParseCursor("bm90LWEtY3Vyc29y") // well formed, but not this store's
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.List(t.Context(), item.Page{Limit: 10, Cursor: c}); !errors.Is(err, item.ErrInvalidCursor) {
			t.Errorf("List with a foreign cursor: %v, want ErrInvalidCursor", err)
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
		list, err := listAll(t, s)
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

// listAll follows a store's pages to the end, as a client would, and
// returns every item.
func listAll(t *testing.T, s item.Store) ([]item.Item, error) {
	t.Helper()
	var all []item.Item
	p := item.Page{Limit: item.MaxPageSize}
	for range 1000 {
		l, err := s.List(t.Context(), p)
		if err != nil {
			return nil, err
		}
		all = append(all, l.Items...)
		if l.Next.IsZero() {
			return all, nil
		}
		p.Cursor = l.Next
	}
	t.Fatal("a thousand pages and no end: the cursor doesn't move")
	return nil, nil
}

func sortedIDs(ids []item.ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	slices.Sort(out)
	return out
}
