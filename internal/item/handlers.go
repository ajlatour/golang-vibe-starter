package item

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ajlatour/golang-vibe-starter/internal/featureflag"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
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

// RegisterRoutes adds the /items CRUD routes to mux, backed by store.
func RegisterRoutes(mux *http.ServeMux, logger *slog.Logger, store Store, opts ...Option) {
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

func handleList(logger *slog.Logger, store Store, flags Flags) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		resp := map[string]any{"items": items}
		if flags != nil && flags.Bool(r.Context(), FlagListTotal) {
			resp["total"] = len(items)
		}
		httpx.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleGet(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it, err := store.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, it)
	}
}

func handleCreate(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeInput(w, r)
		if !ok {
			return
		}
		it, err := store.Create(r.Context(), Item{Name: in.Name, Description: in.Description})
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		w.Header().Set("Location", "/items/"+it.ID)
		httpx.WriteJSON(w, http.StatusCreated, it)
	}
}

func handleUpdate(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeInput(w, r)
		if !ok {
			return
		}
		it, err := store.Update(r.Context(), Item{ID: r.PathValue("id"), Name: in.Name, Description: in.Description})
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, it)
	}
}

func handleDelete(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete(r.Context(), r.PathValue("id")); err != nil {
			storeError(logger, w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// decodeInput reads and validates the JSON request body shared by create and
// update. If it can't, it has already written the 400 (unreadable body) or 422
// (invalid fields) response and returns false.
func decodeInput(w http.ResponseWriter, r *http.Request) (input, bool) {
	var in input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return input{}, false
	}
	if msg := in.validate(); msg != "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, msg)
		return input{}, false
	}
	return in, true
}

// storeError maps ErrNotFound to 404 and anything else to the status
// httpx.ServerError picks: 500, or 502/504 for a failed upstream call.
func storeError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, ErrNotFound.Error())
		return
	}
	httpx.ServerError(logger, w, r, err)
}
