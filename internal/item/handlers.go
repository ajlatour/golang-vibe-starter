package item

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// FlagListTotal is the feature flag that adds a "total" count to the list
// response.
const FlagListTotal = "items-list-total"

// FlagKeys lists every feature flag the handlers read. The service checks at
// startup that flagd/flags.json defines each (cmd/api), so a flag read here
// always has a default to fall back on. Add a flag the handlers read to it.
func FlagKeys() []string {
	return []string{FlagListTotal}
}

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

// Request is the body of a create or replace, as the API accepts it: what
// NewDraft makes a Draft of.
type Request struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Response is an item as the API sends it. The model has no JSON shape of its
// own, so the API's can change without it, and it without the API's.
type Response struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NewResponse is it as the API sends it.
func NewResponse(it Item) Response {
	return Response{ID: it.ID.String(), Name: it.Name.String(), Description: it.Description.String(), CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
}

func handleList(logger *slog.Logger, store Store, flags Flags) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		out := make([]Response, len(items))
		for i, it := range items {
			out[i] = NewResponse(it)
		}
		resp := map[string]any{"items": out}
		if flags != nil && flags.Bool(r.Context(), FlagListTotal) {
			resp["total"] = len(items)
		}
		httpx.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleGet(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		it, err := store.Get(r.Context(), id)
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, NewResponse(it))
	}
}

func handleCreate(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, ok := decodeDraft(w, r)
		if !ok {
			return
		}
		it, err := store.Create(r.Context(), d)
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		w.Header().Set("Location", "/items/"+it.ID.String())
		httpx.WriteJSON(w, http.StatusCreated, NewResponse(it))
	}
}

func handleUpdate(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		d, ok := decodeDraft(w, r)
		if !ok {
			return
		}
		it, err := store.Update(r.Context(), id, d)
		if err != nil {
			storeError(logger, w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, NewResponse(it))
	}
}

func handleDelete(logger *slog.Logger, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := store.Delete(r.Context(), id); err != nil {
			storeError(logger, w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// pathID returns the request's {id}. If it isn't an ID (see ID), no item can
// have it: pathID answers 404, as for any unknown item, without asking the
// store, and returns false.
func pathID(w http.ResponseWriter, r *http.Request) (ID, bool) {
	id, err := ParseID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, ErrNotFound.Error())
		return ID{}, false
	}
	return id, true
}

// decodeDraft reads the JSON request body shared by create and update, and
// makes a Draft of it. If it can't, it has already written the 400 (unreadable
// body) or 422 (a field breaks a rule) response and returns false.
func decodeDraft(w http.ResponseWriter, r *http.Request) (Draft, bool) {
	var req Request
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidBody, err.Error())
		return Draft{}, false
	}
	d, err := NewDraft(req.Name, req.Description)
	if err != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeInvalidField, err.Error())
		return Draft{}, false
	}
	return d, true
}

// storeError answers a Store's error: ErrNotFound is a 404, ErrStoreTimeout a
// 504, ErrStoreUnavailable a 502, and anything else a 500, each 5xx logged by
// httpx.ServerError.
func storeError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, ErrNotFound.Error())
	case errors.Is(err, ErrStoreTimeout):
		httpx.ServerError(logger, w, r, withStatus{err, http.StatusGatewayTimeout})
	case errors.Is(err, ErrStoreUnavailable):
		httpx.ServerError(logger, w, r, withStatus{err, http.StatusBadGateway})
	default:
		httpx.ServerError(logger, w, r, err)
	}
}

// withStatus gives err the status httpx.ServerError should answer it with,
// keeping err's chain (and so its log fields) for errors.As.
type withStatus struct {
	error
	status int
}

func (e withStatus) HTTPStatus() int { return e.status }
func (e withStatus) Unwrap() error   { return e.error }

var _ httpx.StatusError = withStatus{}
