// Package item is the placeholder resource the API serves: the domain model
// (Item, and the value objects it's made of), the Store interface its handlers
// depend on, and the /items handlers with the JSON shapes they speak. The
// production store is in the httpstore subpackage, which reads and writes
// items on an upstream speaking the same /items contract (WireMock in local
// development); itemtest has an in-memory one for tests.
//
// The model enforces its own rules: an ID, a Name or a Description can only be
// made valid, by ParseID, NewName and NewDescription, and a Draft, what a
// client asks to create or replace, only from a valid name and description.
// The JSON shapes stay at the edges (the handlers' Request and Response, and
// httpstore's own types for the upstream), so neither the API's wire format
// nor the upstream's reaches the model. See ADR-0030.
package item

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrNotFound is returned by a Store when no item has the given ID.
var ErrNotFound = errors.New("item not found")

// A Store's errors match these, with errors.Is, when it couldn't do its part:
// it didn't answer in time, or couldn't give a usable answer (it failed, was
// unreachable, or answered with something unusable). The handlers answer them
// 504 and 502, which say the API's dependency failed, not the API; any other
// error is a 500. A Store says what went wrong, and the handlers decide what
// that means to the client.
var (
	ErrStoreTimeout     = errors.New("the item store didn't answer in time")
	ErrStoreUnavailable = errors.New("the item store couldn't give a usable answer")
)

// MaxIDLen is the longest an item ID can be.
const MaxIDLen = 128

// ID names an item: 1 to MaxIDLen ASCII letters, digits, '-' and '_'. No item
// has any other ID, so the handlers answer 404 for one without asking the
// store, and httpstore never puts one in an upstream URL. That matters because
// an upstream, or a proxy in front of it, may normalize "." and ".." path
// segments or decode %2F, so an ID such as ".." or "..%2F..%2Fadmin" could
// otherwise reach a route other than /items/{id}.
//
// The zero ID is no item's; ParseID makes the others.
type ID struct{ v string }

// ErrInvalidID is ParseID's error: no item can have such an ID.
var ErrInvalidID = errors.New("not an item ID")

// ParseID returns s as an ID, or ErrInvalidID.
func ParseID(s string) (ID, error) {
	if s == "" || len(s) > MaxIDLen {
		return ID{}, ErrInvalidID
	}
	for _, c := range []byte(s) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return ID{}, ErrInvalidID
		}
	}
	return ID{s}, nil
}

func (id ID) String() string { return id.v }

// IsZero reports whether id is the zero ID, which names no item.
func (id ID) IsZero() bool { return id.v == "" }

// MaxNameLen is the longest allowed item name, in characters.
const MaxNameLen = 200

// MaxDescriptionLen is the longest allowed item description, in characters.
const MaxDescriptionLen = 2000

// ValidationError is a value that breaks one of the model's rules. Its message
// is written for the client: the handlers send it as a 422's error.
type ValidationError struct {
	field, msg string
}

func (e *ValidationError) Error() string { return e.msg }

// Field is the field that breaks the rule: "name" or "description".
func (e *ValidationError) Field() string { return e.field }

// The rules' errors, made once, so refusing a client's input allocates
// nothing; ValidationError has no exported fields, so no caller can change
// them for everyone.
var (
	errNameRequired    = &ValidationError{field: "name", msg: "name is required"}
	errNameTooLong     = &ValidationError{field: "name", msg: fmt.Sprintf("name must be at most %d characters", MaxNameLen)}
	errDescriptionLong = &ValidationError{field: "description", msg: fmt.Sprintf("description must be at most %d characters", MaxDescriptionLen)}
)

// Name is an item's name: 1 to MaxNameLen characters, without surrounding
// whitespace.
type Name struct{ v string }

// NewName returns s, trimmed of surrounding whitespace, as a Name, or a
// *ValidationError.
func NewName(s string) (Name, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Name{}, errNameRequired
	case utf8.RuneCountInString(s) > MaxNameLen:
		return Name{}, errNameTooLong
	}
	return Name{s}, nil
}

func (n Name) String() string { return n.v }

// Description is an item's description: at most MaxDescriptionLen characters,
// and may be empty.
type Description struct{ v string }

// NewDescription returns s as a Description, or a *ValidationError.
func NewDescription(s string) (Description, error) {
	if utf8.RuneCountInString(s) > MaxDescriptionLen {
		return Description{}, errDescriptionLong
	}
	return Description{s}, nil
}

func (d Description) String() string { return d.v }

// Draft is what a client asks to create, or to replace an item with: a valid
// name and description. NewDraft makes one; the zero Draft has no name, and a
// Store needn't accept it.
type Draft struct {
	name        Name
	description Description
}

// NewDraft returns a draft of name and description, or the *ValidationError
// of the first that breaks a rule, the name's first.
func NewDraft(name, description string) (Draft, error) {
	n, err := NewName(name)
	if err != nil {
		return Draft{}, err
	}
	d, err := NewDescription(description)
	if err != nil {
		return Draft{}, err
	}
	return Draft{name: n, description: d}, nil
}

func (d Draft) Name() Name               { return d.name }
func (d Draft) Description() Description { return d.description }

// Item is a placeholder for the real domain model.
type Item struct {
	ID          ID
	Name        Name
	Description Description
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ErrInvalidItem is an item a Store sent that breaks the model's rules: an
// ID no item can have, a name or description out of bounds, or no
// timestamps. Parse says which.
var ErrInvalidItem = errors.New("the item breaks the model's rules")

// Parse rebuilds an item a Store sent from outside the service, holding it to
// the model's rules, as NewDraft holds a client's: a valid ID, a name and a
// description within their limits, and both timestamps. An item that breaks
// one is ErrInvalidItem, saying which; the values aren't quoted, since they
// came from outside. httpstore uses it, so the API never passes on an item
// it couldn't have made itself.
func Parse(id, name, description string, createdAt, updatedAt time.Time) (Item, error) {
	pid, err := ParseID(id)
	if err != nil {
		return Item{}, fmt.Errorf("%w: its ID isn't an item ID", ErrInvalidItem)
	}
	n, err := NewName(name)
	if err != nil || n.String() != name {
		return Item{}, fmt.Errorf("%w: its name is missing, too long, or untrimmed", ErrInvalidItem)
	}
	d, err := NewDescription(description)
	if err != nil {
		return Item{}, fmt.Errorf("%w: its description is too long", ErrInvalidItem)
	}
	if createdAt.IsZero() || updatedAt.IsZero() {
		return Item{}, fmt.Errorf("%w: it has no created_at or updated_at", ErrInvalidItem)
	}
	return Item{ID: pid, Name: n, Description: d, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

// Restore rebuilds an item a Store holds, as the store has it, without
// checking the rules: for a store whose items can only have come through the
// model (the in-memory one, test seeds). A store holding items from outside
// the service uses Parse.
func Restore(id, name, description string, createdAt, updatedAt time.Time) Item {
	return Item{ID: ID{id}, Name: Name{name}, Description: Description{description}, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

// The list's pages: how many items a page holds without a limit, and at most.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// MaxCursorLen bounds a cursor: a Store's are short, and a client's is
// checked before it reaches one.
const MaxCursorLen = 256

// Cursor marks a place in the list, after the last item of a page: made by a
// Store, opaque to everyone else, and given back to list on from there. The
// zero Cursor is the start of the list. ParseCursor checks a client's: 1 to
// MaxCursorLen URL-safe base64 characters, so it's safe in an upstream URL;
// whether it's one the Store made is the Store's to say (ErrInvalidCursor).
type Cursor struct{ v string }

// ErrInvalidCursor is a cursor that marks no place in the list: not one a
// Store made, or not in the form they take.
var ErrInvalidCursor = errors.New("the cursor isn't valid")

// ParseCursor returns s as a Cursor, or ErrInvalidCursor; "" is the start.
func ParseCursor(s string) (Cursor, error) {
	if len(s) > MaxCursorLen {
		return Cursor{}, ErrInvalidCursor
	}
	for _, c := range []byte(s) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return Cursor{}, ErrInvalidCursor
		}
	}
	return Cursor{s}, nil
}

// NewCursor is a Store's cursor, in the form ParseCursor accepts.
func NewCursor(s string) Cursor { return Cursor{s} }

func (c Cursor) String() string { return c.v }

// IsZero reports whether c is the start of the list, or, as a Listing's Next,
// that there's no next page.
func (c Cursor) IsZero() bool { return c.v == "" }

// Page asks for one page of the list: up to Limit items (1 to MaxPageSize),
// after Cursor.
type Page struct {
	Limit  int
	Cursor Cursor
}

// Listing is one page of the list, oldest first, and where the next page
// starts: the zero Cursor on the last page.
type Listing struct {
	Items []Item // non-nil
	Next  Cursor
}

// Store is the persistence boundary for items. Handlers depend only on this
// interface. Implementations assign ID and timestamps, and must be safe for
// concurrent use.
type Store interface {
	// List returns a page of the items, oldest first: up to p.Limit, after
	// p.Cursor, with the cursor for the next page. A cursor it didn't make
	// is ErrInvalidCursor. A page stays right when items are added or
	// deleted between calls: nothing is skipped, or listed twice.
	List(ctx context.Context, p Page) (Listing, error)
	Get(ctx context.Context, id ID) (Item, error)
	// Create stores a new item made from d and returns it with ID,
	// CreatedAt and UpdatedAt set.
	Create(ctx context.Context, d Draft) (Item, error)
	// Update replaces the name and description of the item with id by d's,
	// bumps UpdatedAt, and returns the stored item.
	Update(ctx context.Context, id ID, d Draft) (Item, error)
	Delete(ctx context.Context, id ID) error
}
