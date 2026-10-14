package memstore_test

import (
	"errors"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/item/memstore"
)

// TestStore holds the in-memory store to the contract every store keeps:
// the service serves with it (STORE=memory), and the httpstore tests use it
// as their upstream.
func TestStore(t *testing.T) {
	itemtest.StoreContract(t, func(t *testing.T) item.Store { return memstore.New() })
}

// TestStoreIsBounded checks a create beyond MaxItems is refused, and that
// deleting one makes room again.
func TestStoreIsBounded(t *testing.T) {
	s := memstore.New()
	d := itemtest.MustDraft("x", "")
	var last item.Item
	for i := range memstore.MaxItems {
		it, err := s.Create(t.Context(), d)
		if err != nil {
			t.Fatalf("create %d: %v", i+1, err)
		}
		last = it
	}
	if _, err := s.Create(t.Context(), d); !errors.Is(err, memstore.ErrFull) {
		t.Fatalf("create past MaxItems: %v, want ErrFull", err)
	}
	if err := s.Delete(t.Context(), last.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), d); err != nil {
		t.Errorf("create after a delete: %v", err)
	}
}
