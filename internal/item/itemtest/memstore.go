// Package itemtest provides test helpers for code that uses item.Store. It
// is imported only by tests.
package itemtest

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// MemStore is an in-memory item.Store. Tests use it on its own, or behind
// item.RegisterRoutes on an httptest server as a stand-in upstream for
// httpstore.Store, so no container is needed.
type MemStore struct {
	mu    sync.RWMutex
	items map[item.ID]item.Item
	now   func() time.Time
}

var _ item.Store = (*MemStore)(nil)

func NewMemStore() *MemStore {
	return &MemStore{items: make(map[item.ID]item.Item), now: time.Now}
}

// Seed stores items exactly as given, IDs and timestamps included, replacing
// any with the same ID. It lets a test start from known data, such as the
// items the WireMock stubs serve; item.Restore makes them.
func (s *MemStore) Seed(items ...item.Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range items {
		s.items[it.ID] = it
	}
}

func (s *MemStore) List(_ context.Context) ([]item.Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]item.Item, 0, len(s.items))
	for _, it := range s.items {
		items = append(items, it)
	}
	slices.SortFunc(items, func(a, b item.Item) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	return items, nil
}

func (s *MemStore) Get(_ context.Context, id item.ID) (item.Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	if !ok {
		return item.Item{}, item.ErrNotFound
	}
	return it, nil
}

func (s *MemStore) Create(_ context.Context, d item.Draft) (item.Item, error) {
	if d.Name().String() == "" {
		return item.Item{}, errZeroDraft
	}
	// rand.Text is 26 letters and digits: always an ID.
	id, err := item.ParseID(rand.Text())
	if err != nil {
		return item.Item{}, err
	}
	now := s.now().UTC()
	it := item.Item{ID: id, Name: d.Name(), Description: d.Description(), CreatedAt: now, UpdatedAt: now}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[it.ID] = it
	return it, nil
}

func (s *MemStore) Update(_ context.Context, id item.ID, d item.Draft) (item.Item, error) {
	if d.Name().String() == "" {
		return item.Item{}, errZeroDraft
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.items[id]
	if !ok {
		return item.Item{}, item.ErrNotFound
	}
	cur.Name = d.Name()
	cur.Description = d.Description()
	cur.UpdatedAt = s.now().UTC()
	s.items[cur.ID] = cur
	return cur, nil
}

func (s *MemStore) Delete(_ context.Context, id item.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return item.ErrNotFound
	}
	delete(s.items, id)
	return nil
}

// errZeroDraft is the zero item.Draft's refusal: it has no name, so only a bug
// in a caller, which should have used item.NewDraft, sends one.
var errZeroDraft = errors.New("itemtest: a zero item.Draft; make one with item.NewDraft")

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
