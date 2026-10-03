package item

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// FlagListTotal is the feature flag that adds a "total" count to the list
// response. Off by default.
const FlagListTotal = "items-list-total"

// Flags evaluates boolean feature flags. Implementations return def when a
// flag can't be evaluated, so handlers never handle flag errors.
type Flags interface {
	Bool(ctx context.Context, key string, def bool) bool
}

// Option customizes RegisterRoutes.
type Option func(*options)

type options struct{ flags Flags }

// WithFlags enables feature flags. Without it every flag takes its default.
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
			httpx.ServerError(logger, w, r, err)
			return
		}
		resp := map[string]any{"items": items}
		if flags != nil && flags.Bool(r.Context(), FlagListTotal, false) {
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
		var in input
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if msg := in.validate(); msg != "" {
			httpx.WriteError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		it, err := store.Create(r.Context(), Item{Name: in.Name, Description: in.Description})
		if err != nil {
			httpx.ServerError(logger, w, r, err)
			return
		}
		w.Header().Set("Location", "/items/"+it.ID)
		httpx.WriteJSON(w, http.StatusCreated, it)
	}
}

func handleUpdate(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in input
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if msg := in.validate(); msg != "" {
			httpx.WriteError(w, http.StatusUnprocessableEntity, msg)
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

// storeError maps ErrNotFound to 404 and anything else to 500.
func storeError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, ErrNotFound.Error())
		return
	}
	httpx.ServerError(logger, w, r, err)
}
