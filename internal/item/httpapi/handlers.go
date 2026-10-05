// Package httpapi is the HTTP side of the item domain: the /items routes. It
// decodes requests into domain values (so the domain's rules decide what is
// valid), calls the item.Store, and encodes items as the API's JSON, which is
// defined here and nowhere in the domain.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/featureflag"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// FlagListTotal is the feature flag that adds a "total" count to the list
// response. Declaring it here makes the service check at startup that
// flagd/flags.json defines it.
var FlagListTotal = featureflag.Declare("items-list-total")

// Flags evaluates boolean feature flags. Implementations fall back to a
// configured default when a flag can't be evaluated, so handlers never handle
// flag errors.
type Flags interface {
	Bool(ctx context.Context, key string) bool
}

// Option customizes RegisterRoutes.
type Option func(*options)

type options struct{ flags Flags }

// WithFlags enables feature flags. Without it every flag is off.
func WithFlags(f Flags) Option {
	return func(o *options) { o.flags = f }
}

// ItemResponse is an item as the API returns it: the JSON in
// docs/openapi.json's Item schema.
type ItemResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListResponse is the body of GET /items. Total is set only when the
// items-list-total flag is on.
type ListResponse struct {
	Items []ItemResponse `json:"items"`
	Total *int           `json:"total,omitempty"`
}

// ItemRequest is the body of a create or replace: the JSON in the ItemInput
// schema.
type ItemRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func toResponse(it item.Item) ItemResponse {
	return ItemResponse{
		ID:          it.ID,
		Name:        it.Name.String(),
		Description: it.Description,
		CreatedAt:   it.CreatedAt,
		UpdatedAt:   it.UpdatedAt,
	}
}

// RegisterRoutes adds the /items CRUD routes to mux, backed by store.
func RegisterRoutes(mux *http.ServeMux, logger *slog.Logger, store item.Store, opts ...Option) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	mux.Handle("GET /items", handleList(logger, store, o.flags))
	mux.Handle("POST /items", handleCreate(logger, store))
	mux.Handle("GET /items/{id}", handleGet(logger, store))
	mux.Handle("PUT /items/{id}", handleUpdate(logger, store))
	mux.Handle("DELETE /items/{id}", handleDelete(logger, store))
}

func handleList(logger *slog.Logger, store item.Store, flags Flags) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		resp := ListResponse{Items: make([]ItemResponse, len(items))}
		for i, it := range items {
			resp.Items[i] = toResponse(it)
		}
		if flags != nil && flags.Bool(r.Context(), FlagListTotal) {
			total := len(items)
			resp.Total = &total
		}
		httpx.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleGet(logger *slog.Logger, store item.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it, err := store.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, toResponse(it))
	}
}

func handleCreate(logger *slog.Logger, store item.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it, ok := decodeItem(w, r)
		if !ok {
			return
		}
		created, err := store.Create(r.Context(), it)
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		w.Header().Set("Location", "/items/"+created.ID)
		httpx.WriteJSON(w, http.StatusCreated, toResponse(created))
	}
}

func handleUpdate(logger *slog.Logger, store item.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it, ok := decodeItem(w, r)
		if !ok {
			return
		}
		it.ID = r.PathValue("id")
		updated, err := store.Update(r.Context(), it)
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, toResponse(updated))
	}
}

func handleDelete(logger *slog.Logger, store item.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete(r.Context(), r.PathValue("id")); err != nil {
			storeError(logger, w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// decodeItem reads the JSON body shared by create and replace and turns it
// into a domain Item, so the domain's rules decide what's valid. If it can't,
// it has already written the 400 (unreadable body) or 422 (a broken rule)
// response and returns false.
func decodeItem(w http.ResponseWriter, r *http.Request) (item.Item, bool) {
	var req ItemRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return item.Item{}, false
	}
	name, err := item.NewName(req.Name)
	if err != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return item.Item{}, false
	}
	return item.Item{Name: name, Description: req.Description}, true
}

// storeError maps the domain's errors to statuses: ErrNotFound to 404, a
// broken rule to 422, and anything else to the status httpx.ServerError
// picks: 500, or 502/504 for a failed upstream call.
func storeError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	var invalid *item.ValidationError
	switch {
	case errors.Is(err, item.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, item.ErrNotFound.Error())
	case errors.As(err, &invalid):
		httpx.WriteError(w, http.StatusUnprocessableEntity, invalid.Error())
	default:
		httpx.ServerError(logger, w, r, err)
	}
}
