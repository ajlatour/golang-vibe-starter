// Package httpstore is the item.Store the API uses in production: it reads
// and writes items on an upstream service over HTTP, logging each call and
// reporting failures as *UpstreamError, which the handlers turn into 502 or
// 504.
package httpstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/jsoncall"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// Store is an item.Store backed by an upstream service that speaks the same
// /items JSON API that item.RegisterRoutes serves: WireMock stubs in local
// development, later a real backend. Switching upstreams is a base URL change.
type Store struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
	retries int          // see WithRetries
	budget  *retryBudget // shared by every call
}

var _ item.Store = (*Store)(nil)

// maxLoggedUpstreamMessage caps the upstream's own error message, the one
// piece of an upstream response body that is quoted in errors and logs.
const maxLoggedUpstreamMessage = 200

// maxResponseBytes caps how much of an upstream response is read.
const maxResponseBytes = 4 << 20 // 4 MiB

// Option customizes New.
type Option func(*Store)

// WithLogger makes the store log every upstream call: the request going
// out and the response coming back at info, a failed call at warn, and a call
// abandoned because the client left at debug. The default is no logging.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Store) { s.logger = logger }
}

// New returns a store that sends requests to baseURL (for example
// "http://mock:8080", optionally with a path prefix) using client. The
// client should have a timeout set.
func New(baseURL string, client *http.Client, opts ...Option) *Store {
	s := &Store{baseURL: strings.TrimRight(baseURL, "/"), client: client, logger: slog.New(slog.DiscardHandler), budget: newRetryBudget()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Store) List(ctx context.Context, p item.Page) (item.Listing, error) {
	var out struct {
		Items      []upstreamItem `json:"items"`
		NextCursor string         `json:"next_cursor"`
	}
	// Built by hand: a limit is digits and a cursor URL-safe characters
	// (item.ParseCursor), so neither needs escaping, and url.Values would
	// cost a map and its encoding on every list.
	path := "/items?limit=" + strconv.Itoa(p.Limit)
	if !p.Cursor.IsZero() {
		path += "&cursor=" + p.Cursor.String()
	}
	if err := s.do(ctx, http.MethodGet, path, nil, http.StatusOK, &out); err != nil {
		return item.Listing{}, err
	}
	l := item.Listing{Items: make([]item.Item, len(out.Items))}
	for i, u := range out.Items {
		l.Items[i] = u.item()
	}
	// The upstream's cursor goes back to it as it came; only a malformed
	// one is dropped, which ends the list here rather than send it on.
	if next, err := item.ParseCursor(out.NextCursor); err == nil {
		l.Next = next
	}
	return l, nil
}

func (s *Store) Get(ctx context.Context, id item.ID) (item.Item, error) {
	if id.IsZero() {
		return item.Item{}, item.ErrNotFound
	}
	var out upstreamItem
	if err := s.do(ctx, http.MethodGet, itemPath(id), nil, http.StatusOK, &out); err != nil {
		return item.Item{}, err
	}
	return out.item(), nil
}

func (s *Store) Create(ctx context.Context, d item.Draft) (item.Item, error) {
	var out upstreamItem
	if err := s.do(ctx, http.MethodPost, "/items", newUpstreamInput(d), http.StatusCreated, &out); err != nil {
		return item.Item{}, err
	}
	return out.item(), nil
}

func (s *Store) Update(ctx context.Context, id item.ID, d item.Draft) (item.Item, error) {
	if id.IsZero() {
		return item.Item{}, item.ErrNotFound
	}
	var out upstreamItem
	if err := s.do(ctx, http.MethodPut, itemPath(id), newUpstreamInput(d), http.StatusOK, &out); err != nil {
		return item.Item{}, err
	}
	return out.item(), nil
}

func (s *Store) Delete(ctx context.Context, id item.ID) error {
	if id.IsZero() {
		return item.ErrNotFound
	}
	return s.do(ctx, http.MethodDelete, itemPath(id), nil, http.StatusNoContent, nil)
}

// upstreamItem is an item as the upstream sends it. It's the upstream's own
// shape, translated here, so the model depends on neither it nor the API's:
// the two happen to match today, and needn't.
type upstreamItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// item is u in the model's terms. The upstream is where items are kept, so
// they're restored as it has them (see item.Restore).
func (u upstreamItem) item() item.Item {
	return item.Restore(u.ID, u.Name, u.Description, u.CreatedAt, u.UpdatedAt)
}

// upstreamInput is the request body sent upstream to create or replace an
// item.
type upstreamInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func newUpstreamInput(d item.Draft) upstreamInput {
	return upstreamInput{Name: d.Name().String(), Description: d.Description().String()}
}

// itemPath is the upstream path of the item with id. An item.ID can only hold
// a valid ID (see item.ID), so the escaping is a second line of defence.
func itemPath(id item.ID) string {
	return "/items/" + url.PathEscape(id.String())
}

// do sends a request with an optional JSON body and decodes a JSON response
// into out (if non-nil). A JSON 404 becomes item.ErrNotFound; any other status
// than want is an *UpstreamError that includes the upstream's error message.
//
// Each call logs up to three lines: "upstream request sent" before it goes
// out; "upstream response received" once a response has arrived, whatever its
// status; and, if the call failed, one more line saying so (see
// WithLogger). Never headers or bodies.
func (s *Store) do(ctx context.Context, method, path string, body any, want int, out any) error {
	// The query string (a list's limit and cursor, which is the client's)
	// is never logged, like any request's.
	call := method + " " + logx.Truncate(pathOnly(path), logx.MaxLoggedPath)
	s.budget.earn()
	var status int
	var err error
	var elapsed time.Duration
	var attrs []slog.Attr
	for attempt := 1; ; attempt++ {
		s.logger.LogAttrs(ctx, slog.LevelInfo, "upstream request sent", slog.String(logx.KeyUpstream, call))
		start := time.Now()
		status, err = s.roundTrip(ctx, method, path, body, want, out)
		elapsed = time.Since(start)
		attrs = upstreamAttrs(call, status, elapsed)
		if status != 0 {
			s.logger.LogAttrs(ctx, slog.LevelInfo, "upstream response received", attrs...)
		}
		if err == nil || attempt > s.retries || !retriable(ctx, method, err) || !s.budget.spend() {
			break
		}
		s.logger.LogAttrs(ctx, slog.LevelInfo, "upstream request retried",
			append(attrs, slog.Int(logx.KeyAttempt, attempt+1), slog.Any(logx.KeyError, err))...)
		if !pause(ctx) {
			break
		}
	}

	switch {
	case err == nil, errors.Is(err, item.ErrNotFound):
		// The upstream answered as the contract allows; "no such item" is
		// not a failure.
		return err
	case errors.Is(ctx.Err(), context.Canceled):
		// The caller went away, which isn't the upstream's fault. Keep the
		// error typed so the handler can tell this was a disconnect.
		s.logger.LogAttrs(ctx, slog.LevelDebug, "upstream request cancelled",
			append(attrs, slog.Any(logx.KeyError, err))...)
	default:
		s.logger.LogAttrs(ctx, slog.LevelWarn, "upstream request failed",
			append(attrs, slog.Any(logx.KeyError, err))...)
	}
	return &UpstreamError{Call: call, Status: status, Duration: elapsed, Err: err}
}

// roundTrip performs one call. It returns the response status (0 if no
// response arrived) alongside any error.
func (s *Store) roundTrip(ctx context.Context, method, path string, body any, want int, out any) (status int, err error) {
	return jsoncall.Call{
		Client: s.client,
		Method: method,
		URL:    s.baseURL + path,
		// Everything that reaches an error message, and so a log line, uses
		// the truncated path.
		Label:            fmt.Sprintf("upstream %s %s", method, logx.Truncate(pathOnly(path), logx.MaxLoggedPath)),
		In:               body,
		Out:              out,
		Want:             want,
		MaxResponseBytes: maxResponseBytes,
		OnStatus:         notFoundIsErrNotFound,
		ErrorDetail:      upstreamErrorDetail,
	}.Do(ctx)
}

// notFoundIsErrNotFound gives a JSON 404 its meaning, "no such item". A
// plain-text 404 means the route itself is missing, i.e. the base URL is
// misconfigured, and stays a generic error.
func notFoundIsErrNotFound(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusNotFound && isJSON(resp.Header):
		return item.ErrNotFound
	case resp.StatusCode == http.StatusBadRequest && isJSON(resp.Header) && resp.Request.URL.Query().Has("cursor"):
		// The upstream refused a list's cursor: one it didn't make, or no
		// longer knows. The limit is always one it takes.
		return item.ErrInvalidCursor
	}
	return nil
}

// upstreamErrorDetail quotes the upstream's own {"error": "..."} message, the
// one piece of an upstream response body that appears in errors and logs.
func upstreamErrorDetail(body io.Reader) string {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(body).Decode(&e)
	return fmt.Sprintf("%q", logx.Truncate(e.Error, maxLoggedUpstreamMessage))
}

func isJSON(h http.Header) bool {
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	return mt == "application/json"
}

// Reachable reports whether the upstream answers HTTP at its base URL. Any
// response counts, an error status included: it says whether this instance
// can reach the upstream (its network, DNS, configuration), not whether the
// upstream is well. Readiness built on the upstream's own health would take
// every instance out of service together whenever the upstream struggled.
// The request goes through the client's transport alone, so a redirect is a
// response like any other, never followed.
func (s *Store) Reachable(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/", nil)
	if err != nil {
		return err
	}
	transport := s.client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return logx.RedactURLError(err)
	}
	_ = resp.Body.Close() // only the answer mattered, not its body
	return nil
}

// pathOnly is path without its query string, as logged and in errors: a
// list's query holds the client's cursor, and query strings are never
// logged.
func pathOnly(path string) string {
	p, _, _ := strings.Cut(path, "?")
	return p
}
