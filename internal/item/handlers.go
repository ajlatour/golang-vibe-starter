package item

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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
	// Each handler returns its error, and httpx.Handle answers it: the
	// client's mistakes as httpx.Errors, everything else as a logged 5xx.
	mux.Handle("GET /items", httpx.Handle(logger, handleList(store, o.flags)))
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

// NewResponse is it as the API sends it.
func NewResponse(it Item) Response {
	return Response{ID: it.ID.String(), Name: it.Name.String(), Description: it.Description.String(), CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
}

func handleList(store Store, flags Flags) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, err := parsePage(r)
		if err != nil {
			return err
		}
		l, err := store.List(r.Context(), p)
		if err != nil {
			return storeErr(err)
		}
		out := make([]Response, len(l.Items))
		for i, it := range l.Items {
			out[i] = NewResponse(it)
		}
		resp := map[string]any{"items": out}
		if !l.Next.IsZero() {
			resp["next_cursor"] = l.Next.String()
		}
		if flags != nil && flags.Bool(r.Context(), FlagListTotal) {
			resp["total"] = len(l.Items)
		}
		httpx.WriteJSON(w, http.StatusOK, resp)
		return nil
	}
}

// The answers to a page asked for wrongly, made once.
var (
	invalidLimit  = httpx.NewError(http.StatusBadRequest, httpx.CodeInvalidParameter, fmt.Sprintf("limit must be a whole number from 1 to %d", MaxPageSize))
	invalidCursor = httpx.NewError(http.StatusBadRequest, httpx.CodeInvalidParameter, "cursor isn't one this list gave out: start again without it")
)

// parsePage reads the page a list request asks for: ?limit (DefaultPageSize
// without it) and ?cursor (the start without it).
func parsePage(r *http.Request) (Page, error) {
	p := Page{Limit: DefaultPageSize}
	if r.URL.RawQuery == "" {
		return p, nil // the first page, at the default size: nothing to parse
	}
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxPageSize {
			return Page{}, invalidLimit
		}
		p.Limit = n
	}
	c, err := ParseCursor(q.Get("cursor"))
	if err != nil {
		return Page{}, invalidCursor
	}
	p.Cursor = c
	return p, nil
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
		httpx.WriteJSON(w, http.StatusOK, NewResponse(it))
		return nil
	}
}

func handleCreate(store Store) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		d, err := decodeDraft(w, r)
		if err != nil {
			return err
		}
		it, err := store.Create(r.Context(), d)
		if err != nil {
			return storeErr(err)
		}
		w.Header().Set("Location", "/items/"+it.ID.String())
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
		d, err := decodeDraft(w, r)
		if err != nil {
			return err
		}
		it, err := store.Update(r.Context(), id, d)
		if err != nil {
			return storeErr(err)
		}
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

// decodeDraft reads the JSON request body shared by create and update, and
// makes a Draft of it, or returns the error to answer: 400 for a body that
// can't be read, 422 for a field that breaks a rule.
func decodeDraft(w http.ResponseWriter, r *http.Request) (Draft, error) {
	var req Request
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return Draft{}, &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeInvalidBody, Message: err.Error(), Err: err}
	}
	d, err := NewDraft(req.Name, req.Description)
	if err != nil {
		if e, ok := invalidField[err]; ok {
			return Draft{}, e
		}
		return Draft{}, &httpx.Error{Status: http.StatusUnprocessableEntity, Code: httpx.CodeInvalidField, Message: err.Error(), Err: err}
	}
	return d, nil
}

// storeErr says what a Store's error means to the client: ErrNotFound is a
// 404, ErrStoreTimeout a 504, ErrStoreUnavailable a 502, and anything else
// a 500. The 5xxs go to httpx.ServerError (through httpx.Handle), which logs
// them and keeps their details from the client.
func storeErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return notFound
	case errors.Is(err, ErrInvalidCursor):
		return invalidCursor
	case errors.Is(err, ErrStoreTimeout):
		return withStatus{err, http.StatusGatewayTimeout}
	case errors.Is(err, ErrStoreUnavailable):
		return withStatus{err, http.StatusBadGateway}
	}
	return err
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
