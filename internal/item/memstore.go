package item

import (
	"cmp"
	"context"
	"crypto/rand"
	"slices"
	"sync"
	"time"
)

// MemStore is an in-memory Store. Data is lost on restart. The mock service
// uses it as its backing data.
type MemStore struct {
	mu    sync.RWMutex
	items map[string]Item
	now   func() time.Time
}

var _ Store = (*MemStore)(nil)

func NewMemStore() *MemStore {
	return &MemStore{items: make(map[string]Item), now: time.Now}
}

func (s *MemStore) List(_ context.Context) ([]Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Item, 0, len(s.items))
	for _, it := range s.items {
		items = append(items, it)
	}
	slices.SortFunc(items, func(a, b Item) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return items, nil
}

func (s *MemStore) Get(_ context.Context, id string) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return it, nil
}

func (s *MemStore) Create(_ context.Context, it Item) (Item, error) {
	now := s.now().UTC()
	it.ID = rand.Text()
	it.CreatedAt = now
	it.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[it.ID] = it
	return it, nil
}

func (s *MemStore) Update(_ context.Context, it Item) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.items[it.ID]
	if !ok {
		return Item{}, ErrNotFound
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
		return ErrNotFound
	}
	delete(s.items, id)
	return nil
}
