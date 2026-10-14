package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The building blocks every resource the API serves repeats: paged lists,
// entity tags and conditional requests, decoding and validating a body, and
// giving a store's error the status it means. internal/item uses them; a new
// resource does the same, so it's its model and its handlers, not a copy of
// item's plumbing. docs/standards.md#http-apis has the rules they follow.

// Paging: how many entries a page holds without a ?limit, and at most, and the
// longest cursor a client may send (a store's are short).
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
	MaxCursorLen    = 256
)

// PageRequest is the page a list request asks for: up to Limit entries,
// after Cursor ("" for the start). Cursor is in the form ValidCursor accepts;
// whether it marks a place in the list is the store's to say.
type PageRequest struct {
	Limit  int
	Cursor string
}

// The answers to a page asked for wrongly, made once.
var (
	errInvalidLimit  = NewError(http.StatusBadRequest, CodeInvalidParameter, fmt.Sprintf("limit must be a whole number from 1 to %d", MaxPageSize))
	errInvalidCursor = NewError(http.StatusBadRequest, CodeInvalidParameter, "cursor isn't one this list gave out: start again without it")
)

// ErrInvalidCursor is the answer to a cursor the store didn't make: 400,
// invalid_parameter. A handler returns it when its store says so.
var ErrInvalidCursor = errInvalidCursor

// ParsePage reads ?limit (DefaultPageSize without it, 1 to MaxPageSize) and
// ?cursor (the start without it). A value out of range, or a cursor not in a
// cursor's form, is an *Error: 400, invalid_parameter.
func ParsePage(r *http.Request) (PageRequest, error) {
	p := PageRequest{Limit: DefaultPageSize}
	if r.URL.RawQuery == "" {
		return p, nil // the first page, at the default size: nothing to parse
	}
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxPageSize {
			return PageRequest{}, errInvalidLimit
		}
		p.Limit = n
	}
	p.Cursor = q.Get("cursor")
	if !ValidCursor(p.Cursor) {
		return PageRequest{}, errInvalidCursor
	}
	return p, nil
}

// ValidCursor reports whether s is in a cursor's form: up to MaxCursorLen
// URL-safe base64 characters ("" is the start). So a client's cursor is safe
// to pass on in an upstream URL.
func ValidCursor(s string) bool {
	if len(s) > MaxCursorLen {
		return false
	}
	for _, c := range []byte(s) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// List is a page of a list as the API sends it: its entries, where the next
// page starts (left out on the last), and, where a resource offers it, how
// many entries the page holds.
type List[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      *int   `json:"total,omitempty"`
}

// ETag is an entity tag for a resource last changed at updated, whose
// client-visible fields are fields: it changes whenever any of them does,
// even twice in one tick of the clock, and is the same for the same
// resource. A strong tag, quoted. FNV-1a, inline and on the stack: hash/fnv,
// or formatting the time, would cost allocations on every response.
func ETag(updated time.Time, fields ...string) string {
	h := uint64(fnvOffset)
	for _, f := range fields {
		h = fnvString(h, f)
	}
	n := uint64(updated.UnixNano())
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

// etagKey is ETag's canonical form, for setting it without canonicalizing
// the name, an allocation, on every response.
var etagKey = http.CanonicalHeaderKey("ETag")

// SetETag sets the response's ETag.
func SetETag(w http.ResponseWriter, tag string) {
	w.Header()[etagKey] = []string{tag}
}

// NotModified reports whether r's If-None-Match matches tag, compared weakly
// (RFC 9110, 13.1.2): the client has this version already, and gets 304.
func NotModified(r *http.Request, tag string) bool {
	inm := r.Header.Get("If-None-Match")
	return inm != "" && ETagMatches(inm, tag, true)
}

// ErrPreconditionFailed answers a write whose If-Match the resource no
// longer matches: 412, precondition_failed.
var ErrPreconditionFailed = NewError(http.StatusPreconditionFailed, CodePreconditionFailed,
	"it has changed since that version: get it again, and send its new ETag")

// IfMatch is a write's If-Match header, "" without one. Read it first, and
// only then the resource's current tag for Precondition, so a write without
// the header costs nothing.
func IfMatch(r *http.Request) string { return r.Header.Get("If-Match") }

// Precondition enforces a write's If-Match (ifMatch, not ""): the resource
// must still be the version the client last saw, tag (or, for "*", exist at
// all), else ErrPreconditionFailed, so a write never overwrites a change the
// client hasn't seen. A resource that doesn't exist matches nothing.
//
// The check reads, then the write follows: two writers racing in between can
// both pass. Closing that needs the store to take the tag itself.
func Precondition(ifMatch, tag string, exists bool) error {
	if !exists || !ETagMatches(ifMatch, tag, false) {
		return ErrPreconditionFailed
	}
	return nil
}

// ETagMatches reports whether a conditional header (If-Match, If-None-Match:
// "*" or a comma-separated list of entity tags) matches tag. If-Match
// compares strongly, so a weak tag (W/"...") never matches; If-None-Match
// weakly, ignoring the W/ (RFC 9110, 13.1).
func ETagMatches(header, tag string, weak bool) bool {
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

// DecodeValid reads a JSON request body into an In, and makes an Out of it
// with valid, the resource's rules: a body that can't be read is 400,
// invalid_body; one valid refuses is 422, invalid_field, with valid's error
// as the message, unless valid returns an *Error itself (one made once, so
// answering it allocates nothing).
func DecodeValid[In, Out any](w http.ResponseWriter, r *http.Request, valid func(In) (Out, error)) (Out, error) {
	var in In
	if err := DecodeJSON(w, r, &in); err != nil {
		var zero Out
		return zero, &Error{Status: http.StatusBadRequest, Code: CodeInvalidBody, Message: err.Error(), Err: err}
	}
	out, err := valid(in)
	if err != nil {
		if e, ok := errors.AsType[*Error](err); ok {
			return out, e
		}
		return out, &Error{Status: http.StatusUnprocessableEntity, Code: CodeInvalidField, Message: err.Error(), Err: err}
	}
	return out, nil
}

// WithStatus gives err the status ServerError answers it with (a 502 or
// 504, say, for a failed or slow dependency), keeping err's chain, and so
// its log fields, for errors.Is and As.
func WithStatus(err error, status int) error { return withStatus{err, status} }

type withStatus struct {
	error
	status int
}

func (e withStatus) HTTPStatus() int { return e.status }
func (e withStatus) Unwrap() error   { return e.error }

var _ StatusError = withStatus{}
