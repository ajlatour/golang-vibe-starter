// Package item is the placeholder resource the API serves: the Item model,
// the Store interface its handlers depend on, and the /items handlers. The
// production store is in the httpstore subpackage, which reads and writes
// items on an upstream speaking the same /items contract (WireMock in local
// development); itemtest has an in-memory one for tests.
package item

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Item is a placeholder for the real domain model.
type Item struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ErrNotFound is returned by a Store when no item has the given ID.
var ErrNotFound = errors.New("item not found")

// MaxIDLen is the longest an item ID can be.
const MaxIDLen = 128

// ValidID reports whether id could name an item: 1 to MaxIDLen ASCII
// letters, digits, '-' and '_'. No item has any other ID, so the handlers
// answer 404 for one without asking the store, and httpstore never puts one
// in an upstream URL. That matters because an upstream, or a proxy in front
// of it, may normalize "." and ".." path segments or decode %2F, so an ID
// such as ".." or "..%2F..%2Fadmin" could otherwise reach a route other than
// /items/{id}.
func ValidID(id string) bool {
	if id == "" || len(id) > MaxIDLen {
		return false
	}
	for _, c := range []byte(id) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// Store is the persistence boundary for items. Handlers depend only on this
// interface. Implementations assign ID and timestamps, and must be safe for
// concurrent use.
type Store interface {
	// List returns all items, oldest first, as a non-nil slice.
	List(ctx context.Context) ([]Item, error)
	Get(ctx context.Context, id string) (Item, error)
	// Create stores a new item using its Name and Description and returns
	// it with ID, CreatedAt and UpdatedAt set.
	Create(ctx context.Context, it Item) (Item, error)
	// Update replaces Name and Description of the item with it.ID, bumps
	// UpdatedAt, and returns the stored item.
	Update(ctx context.Context, it Item) (Item, error)
	Delete(ctx context.Context, id string) error
}

// MaxNameLen is the longest allowed item name, in characters.
const MaxNameLen = 200

// MaxDescriptionLen is the longest allowed item description, in characters.
const MaxDescriptionLen = 2000

// input is the request body for creating or replacing an item. httpstore
// sends the same shape upstream.
type input struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// validate normalises the input and returns a client-facing message if it
// is invalid, or "" if it is fine.
func (in *input) validate() string {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		return "name is required"
	case utf8.RuneCountInString(in.Name) > MaxNameLen:
		return "name must be at most 200 characters"
	case utf8.RuneCountInString(in.Description) > MaxDescriptionLen:
		return "description must be at most 2000 characters"
	}
	return ""
}
