package item

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// RegisterRoutes adds the /items CRUD routes to mux, backed by store.
func RegisterRoutes(mux *http.ServeMux, logger *slog.Logger, store Store) {
	mux.Handle("GET /items", handleList(logger, store))
	mux.Handle("POST /items", handleCreate(logger, store))
	mux.Handle("GET /items/{id}", handleGet(logger, store))
	mux.Handle("PUT /items/{id}", handleUpdate(logger, store))
	mux.Handle("DELETE /items/{id}", handleDelete(logger, store))
}

func handleList(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		if err != nil {
			httpx.ServerError(logger, w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string][]Item{"items": items})
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
