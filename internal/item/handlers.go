package item

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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

// ListResponse is a page of items as the API sends it.
type ListResponse struct {
	Items []Response `json:"items"`
	// NextCursor is where the next page starts; empty, and left out, on the
	// last.
	NextCursor string `json:"next_cursor,omitempty"`
	// Total is the number of items on the page, sent only with the
	// items-list-total flag on.
	Total *int `json:"total,omitempty"`
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
		resp := ListResponse{Items: make([]Response, len(l.Items))}
		for i, it := range l.Items {
			resp.Items[i] = NewResponse(it)
		}
		if !l.Next.IsZero() {
			resp.NextCursor = l.Next.String()
		}
		if flags != nil && flags.Bool(r.Context(), FlagListTotal) {
			n := len(l.Items)
			resp.Total = &n
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
		tag := ETag(it)
		w.Header()[etagKey] = []string{tag}
		// A client holding this version already needn't have it again.
		if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatches(inm, tag, true) {
			w.WriteHeader(http.StatusNotModified)
			return nil
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
		w.Header()[etagKey] = []string{ETag(it)}
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
		if err := checkIfMatch(r, store, id); err != nil {
			return err
		}
		it, err := store.Update(r.Context(), id, d)
		if err != nil {
			return storeErr(err)
		}
		w.Header()[etagKey] = []string{ETag(it)}
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

// etagKey is ETag's canonical form, for setting it without canonicalizing
// the name, an allocation, on every response.
var etagKey = http.CanonicalHeaderKey("ETag")

// ETag is an item's entity tag: it changes whenever the item does. It hashes
// what a client sees change (the name and description) with when it last
// changed, so two changes in one tick of the clock still differ, and the
// same item always has the same tag. A strong tag, quoted, as HTTP has them.
func ETag(it Item) string {
	// FNV-1a, inline and on the stack: hash/fnv's, or formatting the time,
	// would cost allocations on every response.
	h := uint64(fnvOffset)
	h = fnvString(h, it.Name.String())
	h = fnvString(h, it.Description.String())
	n := uint64(it.UpdatedAt.UnixNano())
	for i := range 8 {
		h = (h ^ (n >> (8 * i) & 0xff)) * fnvPrime
	}
	var buf [16]byte // a quote, up to 13 base-36 digits, a quote
	b := append(buf[:0], '"')
	b = strconv.AppendUint(b, h, 36)
	b = append(b, '"')
	return string(b)
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// fnvString mixes s, and a separator after it, into h: the separator keeps
// "ab"+"c" and "a"+"bc" apart.
func fnvString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * fnvPrime
	}
	return (h ^ 0xff) * fnvPrime
}

// preconditionFailed answers a write whose If-Match the item no longer
// matches.
var preconditionFailed = httpx.NewError(http.StatusPreconditionFailed, httpx.CodePreconditionFailed,
	"the item has changed since that version: get it again, and send its new ETag")

// checkIfMatch enforces a write's If-Match: the item must still be the
// version the client last saw (or, for "*", exist at all), else 412, so a
// write never overwrites a change the client hasn't seen. Without the header
// the write goes ahead, as it always did.
//
// The check reads the item, then the write follows: two writers racing in
// between can both pass. Closing that needs the upstream to take If-Match
// itself, which the contract can grow when there's a real upstream; until
// then this catches the common case, a client acting on a stale copy.
func checkIfMatch(r *http.Request, store Store, id ID) error {
	im := r.Header.Get("If-Match")
	if im == "" {
		return nil
	}
	cur, err := store.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		return preconditionFailed // nothing matches an item that isn't there
	}
	if err != nil {
		return storeErr(err)
	}
	if !etagMatches(im, ETag(cur), false) {
		return preconditionFailed
	}
	return nil
}

// etagMatches reports whether a conditional header (If-Match, If-None-Match:
// "*" or a comma-separated list of entity tags) matches tag. If-Match
// compares strongly, so a weak tag (W/"...") never matches; If-None-Match
// weakly, ignoring the W/ (RFC 9110, 13.1).
func etagMatches(header, tag string, weak bool) bool {
	for t := range strings.SplitSeq(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" {
			return true
		}
		if w, ok := strings.CutPrefix(t, "W/"); ok {
			if !weak {
				continue
			}
			t = w
		}
		if t == tag {
			return true
		}
	}
	return false
}
