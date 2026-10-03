// Package itemtest provides test helpers for code that uses item.Store. It
// is imported only by tests.
package itemtest

import (
	"cmp"
	"context"
	"crypto/rand"
	"slices"
	"sync"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// MemStore is an in-memory item.Store. Tests use it on its own, or behind
// item.RegisterRoutes on an httptest server as a stand-in upstream for
// item.HTTPStore, so no container is needed.
type MemStore struct {
	mu    sync.RWMutex
	items map[string]item.Item
	now   func() time.Time
}

var _ item.Store = (*MemStore)(nil)

func NewMemStore() *MemStore {
	return &MemStore{items: make(map[string]item.Item), now: time.Now}
}

func (s *MemStore) List(_ context.Context) ([]item.Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]item.Item, 0, len(s.items))
	for _, it := range s.items {
		items = append(items, it)
	}
	slices.SortFunc(items, func(a, b item.Item) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return items, nil
}

func (s *MemStore) Get(_ context.Context, id string) (item.Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	if !ok {
		return item.Item{}, item.ErrNotFound
	}
	return it, nil
}

func (s *MemStore) Create(_ context.Context, it item.Item) (item.Item, error) {
	now := s.now().UTC()
	it.ID = rand.Text()
	it.CreatedAt = now
	it.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[it.ID] = it
	return it, nil
}

func (s *MemStore) Update(_ context.Context, it item.Item) (item.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.items[it.ID]
	if !ok {
		return item.Item{}, item.ErrNotFound
	}
	cur.Name = it.Name
	cur.Description = it.Description
	cur.UpdatedAt = s.now().UTC()
	s.items[cur.ID] = cur
	return cur, nil
}

func (s *MemStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return item.ErrNotFound
	}
	delete(s.items, id)
	return nil
}
