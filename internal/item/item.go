// Package item is the domain model for the resource the API serves: the Item
// entity, the Name value object that carries its rules, the Store interface
// (the repository) that persistence implements, and the domain errors.
//
// It is pure: it knows nothing about HTTP, JSON, logging or any upstream, and
// a test (domain_test.go) keeps it that way. Everything technical lives in
// subpackages that depend on it, never the reverse:
//
//   - httpapi turns HTTP requests into domain calls and items into JSON.
//   - httpstore is the Store the API uses, backed by an upstream service.
//   - itemtest has an in-memory Store and the contract every Store must pass.
//
// "Item" is a placeholder for a real domain; replace it, keeping this shape.
package item

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Item is the entity: identified by ID, which the Store assigns.
type Item struct {
	ID          string
	Name        Name
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MaxNameLen is the longest allowed item name, in characters.
const MaxNameLen = 200

// Name is an item's name. The only way to get a non-zero Name is NewName, so a
// Name is always valid: trimmed, not blank, at most MaxNameLen characters.
// The zero Name means no name, and a Store refuses an item that has one.
type Name struct{ s string }

// NewName trims raw and checks it against the rules for a name.
func NewName(raw string) (Name, error) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return Name{}, &ValidationError{Field: "name", Message: "name is required"}
	case utf8.RuneCountInString(s) > MaxNameLen:
		return Name{}, &ValidationError{Field: "name", Message: fmt.Sprintf("name must be at most %d characters", MaxNameLen)}
	}
	return Name{s: s}, nil
}

// MustNewName is NewName for names known to be valid, such as constants and
// test data; it panics on an invalid one.
func MustNewName(raw string) Name {
	n, err := NewName(raw)
	if err != nil {
		panic(err)
	}
	return n
}

func (n Name) String() string { return n.s }

// IsZero reports whether n is the zero Name: no name at all.
func (n Name) IsZero() bool { return n.s == "" }

// ValidationError is a broken domain rule. Its message is safe to show a
// client.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// ErrNotFound is returned by a Store when no item has the given ID.
var ErrNotFound = errors.New("item not found")

// CheckStorable reports whether it can be stored: a Store calls it before
// creating or replacing an item, so an item built without NewName can't be
// persisted.
func CheckStorable(it Item) error {
	if it.Name.IsZero() {
		return &ValidationError{Field: "name", Message: "name is required"}
	}
	return nil
}

// Store is the repository for items. The handlers depend only on this
// interface. Implementations assign ID and timestamps, refuse items that fail
// CheckStorable, and must be safe for concurrent use.
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
