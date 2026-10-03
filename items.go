package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Item is the mock resource served by the API. It is a placeholder for the
// real domain model.
type Item struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ErrNotFound is returned by an ItemStore when no item has the given ID.
var ErrNotFound = errors.New("not found")

// ItemStore is the persistence boundary for items. Handlers depend only on
// this interface, so a real datastore can replace memStore without changing
// them. Implementations assign ID and timestamps, and must be safe for
// concurrent use.
type ItemStore interface {
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

const maxItemNameLen = 200

// itemInput is the request body for creating or replacing an item.
type itemInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// validate normalises the input and returns a client-facing message if it
// is invalid, or "" if it is fine.
func (in *itemInput) validate() string {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		return "name is required"
	case utf8.RuneCountInString(in.Name) > maxItemNameLen:
		return "name must be at most 200 characters"
	}
	return ""
}

func handleListItems(logger *slog.Logger, store ItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		if err != nil {
			serverError(logger, w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string][]Item{"items": items})
	}
}

func handleGetItem(logger *slog.Logger, store ItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it, err := store.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, it)
	}
}

func handleCreateItem(logger *slog.Logger, store ItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in itemInput
		if err := decodeJSON(w, r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if msg := in.validate(); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		it, err := store.Create(r.Context(), Item{Name: in.Name, Description: in.Description})
		if err != nil {
			serverError(logger, w, r, err)
			return
		}
		w.Header().Set("Location", "/items/"+it.ID)
		writeJSON(w, http.StatusCreated, it)
	}
}

func handleUpdateItem(logger *slog.Logger, store ItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in itemInput
		if err := decodeJSON(w, r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if msg := in.validate(); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		it, err := store.Update(r.Context(), Item{ID: r.PathValue("id"), Name: in.Name, Description: in.Description})
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, it)
	}
}

func handleDeleteItem(logger *slog.Logger, store ItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete(r.Context(), r.PathValue("id")); err != nil {
			storeError(logger, w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// storeError maps ErrNotFound to 404 and anything else to 500.
func storeError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	serverError(logger, w, r, err)
}
