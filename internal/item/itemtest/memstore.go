// Package itemtest provides test helpers for code that uses item.Store. It
// is imported only by tests.
package itemtest

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
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

func (s *MemStore) List(_ context.Context, p item.Page) (item.Listing, error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return item.Listing{}, err
	}
	s.mu.RLock()
	all := make([]item.Item, 0, len(s.items))
	for _, it := range s.items {
		all = append(all, it)
	}
	s.mu.RUnlock()
	slices.SortFunc(all, compareItems)

	// The page starts after the cursor's place, whatever has been added or
	// deleted since: nothing is skipped, or listed twice.
	start := 0
	if after != nil {
		// The first item strictly after the cursor's place.
		start = sort.Search(len(all), func(i int) bool {
			n := all[i].CreatedAt.UnixNano()
			return n > after.nanos || (n == after.nanos && all[i].ID.String() > after.id)
		})
	}
	page := all[start:min(start+p.Limit, len(all))]
	l := item.Listing{Items: slices.Clone(page)}
	if l.Items == nil {
		l.Items = []item.Item{}
	}
	if start+p.Limit < len(all) && len(page) > 0 {
		l.Next = encodeCursor(page[len(page)-1])
	}
	return l, nil
}

// compareItems orders the list: oldest first, then by ID. Times compare as
// nanoseconds, so a time's location or monotonic reading can't tell two
// equal ones apart.
func compareItems(a, b item.Item) int {
	return cmp.Or(cmp.Compare(a.CreatedAt.UnixNano(), b.CreatedAt.UnixNano()), cmp.Compare(a.ID.String(), b.ID.String()))
}

// position is a place in the list: just after the item created at nanos
// (Unix nanoseconds) with id.
type position struct {
	nanos int64
	id    string
}

// encodeCursor is the cursor for the place after it: its creation time and
// ID, base64url, so it's opaque and safe in a URL.
func encodeCursor(it item.Item) item.Cursor {
	return item.NewCursor(base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.FormatInt(it.CreatedAt.UnixNano(), 10) + "." + it.ID.String())))
}

// decodeCursor reads a cursor encodeCursor made; the zero cursor is the
// start (nil).
func decodeCursor(c item.Cursor) (*position, error) {
	if c.IsZero() {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c.String())
	if err != nil {
		return nil, item.ErrInvalidCursor
	}
	nanos, id, ok := strings.Cut(string(b), ".")
	n, err := strconv.ParseInt(nanos, 10, 64)
	if !ok || err != nil || id == "" {
		return nil, item.ErrInvalidCursor
	}
	return &position{nanos: n, id: id}, nil
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
