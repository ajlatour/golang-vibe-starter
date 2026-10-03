// Package item is the placeholder resource the API serves. The API's routes
// are backed by an HTTPStore that reads and writes items on an upstream
// speaking the same /items contract (WireMock in local development).
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

// input is the request body for creating or replacing an item. HTTPStore
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
	}
	return ""
}
