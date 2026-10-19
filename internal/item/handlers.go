package item

import (
	// feature:flags
	"context"
	// end feature:flags
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// feature:flags

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

// WithFlags enables feature flags. Without it every flag is off.
func WithFlags(f Flags) Option {
	return func(o *options) { o.flags = f }
}

// end feature:flags

// Option customizes RegisterRoutes.
type Option func(*options)

type options struct {
	// feature:flags
	flags Flags
	// end feature:flags
}

// RegisterRoutes adds the /items CRUD routes to mux, backed by store.
func RegisterRoutes(mux *http.ServeMux, logger *slog.Logger, store Store, opts ...Option) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	// Each handler returns its error, and httpx.Handle answers it: the
	// client's mistakes as httpx.Errors, everything else as a logged 5xx.
	mux.Handle("GET /items", httpx.Handle(logger, handleList(store, o)))
	mux.Handle("POST /items", httpx.Handle(logger, handleCreate(store)))
	mux.Handle("GET /items/{id}", httpx.Handle(logger, handleGet(store)))
	mux.Handle("PUT /items/{id}", httpx.Handle(logger, handleUpdate(store)))
	mux.Handle("DELETE /items/{id}", httpx.Handle(logger, handleDelete(store)))
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

// ListResponse is a page of items as the API sends it.
type ListResponse = httpx.List[Response]

// NewResponse is it as the API sends it.
func NewResponse(it Item) Response {
	return Response{ID: it.ID.String(), Name: it.Name.String(), Description: it.Description.String(), CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
}

func handleList(store Store, o options) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		pr, err := httpx.ParsePage(r)
		if err != nil {
			return err
		}
		// ParsePage checked the cursor's form, which is a Cursor's.
		l, err := store.List(r.Context(), Page{Limit: pr.Limit, Cursor: NewCursor(pr.Cursor)})
		if err != nil {
			return storeErr(err)
		}
		resp := ListResponse{Items: make([]Response, len(l.Items))}
		for i, it := range l.Items {
			resp.Items[i] = NewResponse(it)
		}
		if !l.Next.IsZero() {
			resp.NextCursor = l.Next.String()
		}
		// feature:flags
		if o.flags != nil && o.flags.Bool(r.Context(), FlagListTotal) {
			n := len(l.Items)
			resp.Total = &n
		}
		// end feature:flags
		httpx.WriteJSON(w, http.StatusOK, resp)
		return nil
	}
}

func handleGet(store Store) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		it, err := store.Get(r.Context(), id)
		if err != nil {
			return storeErr(err)
		}
		tag := ETag(it)
		httpx.SetETag(w, tag)
		// A client holding this version already needn't have it again.
		if httpx.NotModified(r, tag) {
			w.WriteHeader(http.StatusNotModified)
			return nil
		}
		httpx.WriteJSON(w, http.StatusOK, NewResponse(it))
		return nil
	}
}

func handleCreate(store Store) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		d, err := httpx.DecodeValid(w, r, draft)
		if err != nil {
			return err
		}
		it, err := store.Create(r.Context(), d)
		if err != nil {
			return storeErr(err)
		}
		w.Header().Set("Location", "/items/"+it.ID.String())
		httpx.SetETag(w, ETag(it))
		httpx.WriteJSON(w, http.StatusCreated, NewResponse(it))
		return nil
	}
}

func handleUpdate(store Store) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		d, err := httpx.DecodeValid(w, r, draft)
		if err != nil {
			return err
		}
		if err := checkIfMatch(r, store, id); err != nil {
			return err
		}
		it, err := store.Update(r.Context(), id, d)
		if err != nil {
			return storeErr(err)
		}
		httpx.SetETag(w, ETag(it))
		httpx.WriteJSON(w, http.StatusOK, NewResponse(it))
		return nil
	}
}

func handleDelete(store Store) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := checkIfMatch(r, store, id); err != nil {
			return err
		}
		if err := store.Delete(r.Context(), id); err != nil {
			return storeErr(err)
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

// The client errors with fixed messages, made once, so answering them
// allocates nothing: a 404 for an ID no item has, and a 422 for each of the
// model's rules. Never modified.
var (
	notFound = httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, ErrNotFound.Error())

	invalidField = map[error]*httpx.Error{
		errNameRequired:    httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeInvalidField, errNameRequired.Error()),
		errNameTooLong:     httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeInvalidField, errNameTooLong.Error()),
		errDescriptionLong: httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeInvalidField, errDescriptionLong.Error()),
	}
)

// pathID returns the request's {id}. If it isn't an ID (see ID), no item can
// have it: the error answers 404, as for any unknown item, without asking
// the store.
func pathID(r *http.Request) (ID, error) {
	id, err := ParseID(r.PathValue("id"))
	if err != nil {
		return ID{}, notFound
	}
	return id, nil
}

// draft makes a Draft of a create's or replace's body, by the model's rules,
// answering each broken rule with its error made once (httpx.DecodeValid).
func draft(req Request) (Draft, error) {
	d, err := NewDraft(req.Name, req.Description)
	if e, ok := invalidField[err]; ok {
		return Draft{}, e
	}
	return d, err
}

// checkIfMatch enforces a write's If-Match against the item with id
// (httpx.Precondition): without the header, it reads nothing.
func checkIfMatch(r *http.Request, store Store, id ID) error {
	im := httpx.IfMatch(r)
	if im == "" {
		return nil
	}
	cur, err := store.Get(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.Precondition(im, "", false)
	case err != nil:
		return storeErr(err)
	}
	return httpx.Precondition(im, ETag(cur), true)
}

// storeErr says what a Store's error means to the client: ErrNotFound is a
// 404, ErrInvalidCursor a 400, ErrStoreTimeout a 504, ErrStoreUnavailable a
// 502, and anything else a 500. The 5xxs go to httpx.ServerError (through
// httpx.Handle), which logs them and keeps their details from the client.
func storeErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return notFound
	case errors.Is(err, ErrInvalidCursor):
		return httpx.ErrInvalidCursor
	case errors.Is(err, ErrStoreTimeout):
		return httpx.WithStatus(err, http.StatusGatewayTimeout)
	case errors.Is(err, ErrStoreUnavailable):
		return httpx.WithStatus(err, http.StatusBadGateway)
	}
	return err
}

// ETag is an item's entity tag: it changes whenever the item does, from what
// a client sees change (the name and description) and when it last changed
// (httpx.ETag).
func ETag(it Item) string {
	return httpx.ETag(it.UpdatedAt, it.Name.String(), it.Description.String())
}
