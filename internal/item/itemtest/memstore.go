// Package itemtest provides test helpers for code that uses item.Store. It
// is imported only by tests.
package itemtest

import (
	"fmt"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/memstore"
)

// MemStore is the in-memory item.Store (memstore), by the name tests use.
type MemStore = memstore.Store

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return memstore.New() }

// MustDraft is item.NewDraft for tests: it panics on a draft that breaks a
// rule, which in a test is a mistake in the test.
func MustDraft(name, description string) item.Draft {
	d, err := item.NewDraft(name, description)
	if err != nil {
		panic(err)
	}
	return d
}

// MustID is item.ParseID for tests: it panics on what isn't an ID.
func MustID(s string) item.ID {
	id, err := item.ParseID(s)
	if err != nil {
		panic(fmt.Sprintf("itemtest.MustID(%q): %v", s, err))
	}
	return id
}
