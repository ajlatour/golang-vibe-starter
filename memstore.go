package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"slices"
	"sync"
	"time"
)

// memStore is an in-memory ItemStore. Data is lost on restart.
type memStore struct {
	mu    sync.RWMutex
	items map[string]Item
	now   func() time.Time
}

var _ ItemStore = (*memStore)(nil)

func newMemStore() *memStore {
	return &memStore{items: make(map[string]Item), now: time.Now}
}

// List returns all items, oldest first.
func (s *memStore) List(_ context.Context) ([]Item, error) {
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

func (s *memStore) Get(_ context.Context, id string) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return it, nil
}

func (s *memStore) Create(_ context.Context, it Item) (Item, error) {
	now := s.now().UTC()
	it.ID = rand.Text()
	it.CreatedAt = now
	it.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[it.ID] = it
	return it, nil
}

func (s *memStore) Update(_ context.Context, it Item) (Item, error) {
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

func (s *memStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return ErrNotFound
	}
	delete(s.items, id)
	return nil
}
