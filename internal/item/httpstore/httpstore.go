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
	s := &Store{baseURL: strings.TrimRight(baseURL, "/"), client: client, logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Store) List(ctx context.Context) ([]item.Item, error) {
	var out struct {
		Items []item.Item `json:"items"`
	}
	if err := s.do(ctx, http.MethodGet, "/items", nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []item.Item{}
	}
	return out.Items, nil
}

func (s *Store) Get(ctx context.Context, id string) (item.Item, error) {
	var it item.Item
	err := s.do(ctx, http.MethodGet, itemPath(id), nil, http.StatusOK, &it)
	return it, err
}

func (s *Store) Create(ctx context.Context, it item.Item) (item.Item, error) {
	var out item.Item
	err := s.do(ctx, http.MethodPost, "/items", itemInput{Name: it.Name, Description: it.Description}, http.StatusCreated, &out)
	return out, err
}

func (s *Store) Update(ctx context.Context, it item.Item) (item.Item, error) {
	var out item.Item
	err := s.do(ctx, http.MethodPut, itemPath(it.ID), itemInput{Name: it.Name, Description: it.Description}, http.StatusOK, &out)
	return out, err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	return s.do(ctx, http.MethodDelete, itemPath(id), nil, http.StatusNoContent, nil)
}

// itemInput is the request body sent upstream to create or replace an item:
// the same shape the API's own handlers accept.
type itemInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func itemPath(id string) string {
	return "/items/" + url.PathEscape(id)
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
	call := method + " " + logx.Truncate(path, logx.MaxLoggedPath)
	s.logger.LogAttrs(ctx, slog.LevelInfo, "upstream request sent", slog.String(logx.KeyUpstream, call))

	start := time.Now()
	status, err := s.roundTrip(ctx, method, path, body, want, out)
	elapsed := time.Since(start)
	attrs := upstreamAttrs(call, status, elapsed)

	if status != 0 {
		s.logger.LogAttrs(ctx, slog.LevelInfo, "upstream response received", attrs...)
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
		Label:            fmt.Sprintf("upstream %s %s", method, logx.Truncate(path, logx.MaxLoggedPath)),
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
	if resp.StatusCode == http.StatusNotFound && isJSON(resp.Header) {
		return item.ErrNotFound
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
