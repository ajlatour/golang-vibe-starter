package item

import (
	"bytes"
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

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// HTTPStore is a Store backed by an upstream service that speaks the same
// /items JSON API that RegisterRoutes serves: WireMock stubs in local
// development, later a real backend. Switching upstreams is a base URL change.
type HTTPStore struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

var _ Store = (*HTTPStore)(nil)

// maxLoggedUpstreamMessage caps the upstream's own error message, the one
// piece of an upstream response body that is quoted in errors and logs.
const maxLoggedUpstreamMessage = 200

// maxResponseBytes caps how much of an upstream response is read.
const maxResponseBytes = 4 << 20 // 4 MiB

// HTTPStoreOption customizes NewHTTPStore.
type HTTPStoreOption func(*HTTPStore)

// WithStoreLogger makes the store log every upstream call: the request going
// out and the response coming back at info, a failed call at warn, and a call
// abandoned because the client left at debug. The default is no logging.
func WithStoreLogger(logger *slog.Logger) HTTPStoreOption {
	return func(s *HTTPStore) { s.logger = logger }
}

// NewHTTPStore returns a store that sends requests to baseURL (for example
// "http://mock:8080", optionally with a path prefix) using client. The
// client should have a timeout set.
func NewHTTPStore(baseURL string, client *http.Client, opts ...HTTPStoreOption) *HTTPStore {
	s := &HTTPStore{baseURL: strings.TrimRight(baseURL, "/"), client: client, logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *HTTPStore) List(ctx context.Context) ([]Item, error) {
	var out struct {
		Items []Item `json:"items"`
	}
	if err := s.do(ctx, http.MethodGet, "/items", nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []Item{}
	}
	return out.Items, nil
}

func (s *HTTPStore) Get(ctx context.Context, id string) (Item, error) {
	var it Item
	err := s.do(ctx, http.MethodGet, itemPath(id), nil, http.StatusOK, &it)
	return it, err
}

func (s *HTTPStore) Create(ctx context.Context, it Item) (Item, error) {
	var out Item
	err := s.do(ctx, http.MethodPost, "/items", input{Name: it.Name, Description: it.Description}, http.StatusCreated, &out)
	return out, err
}

func (s *HTTPStore) Update(ctx context.Context, it Item) (Item, error) {
	var out Item
	err := s.do(ctx, http.MethodPut, itemPath(it.ID), input{Name: it.Name, Description: it.Description}, http.StatusOK, &out)
	return out, err
}

func (s *HTTPStore) Delete(ctx context.Context, id string) error {
	return s.do(ctx, http.MethodDelete, itemPath(id), nil, http.StatusNoContent, nil)
}

func itemPath(id string) string {
	return "/items/" + url.PathEscape(id)
}

// do sends a request with an optional JSON body and decodes a JSON response
// into out (if non-nil). A JSON 404 becomes ErrNotFound; any other status
// than want is an *UpstreamError that includes the upstream's error message.
//
// Each call logs up to three lines: "upstream request sent" before it goes
// out; "upstream response received" once a response has arrived, whatever its
// status; and, if the call failed, one more line saying so (see
// WithStoreLogger). Never headers or bodies.
func (s *HTTPStore) do(ctx context.Context, method, path string, body any, want int, out any) error {
	call := method + " " + httpx.Truncate(path, httpx.MaxLoggedPath)
	s.logger.LogAttrs(ctx, slog.LevelInfo, "upstream request sent", slog.String(httpx.LogKeyUpstream, call))

	start := time.Now()
	status, err := s.roundTrip(ctx, method, path, body, want, out)
	elapsed := time.Since(start)
	attrs := upstreamAttrs(call, status, elapsed)

	if status != 0 {
		s.logger.LogAttrs(ctx, slog.LevelInfo, "upstream response received", attrs...)
	}

	switch {
	case err == nil, errors.Is(err, ErrNotFound):
		// The upstream answered as the contract allows; "no such item" is
		// not a failure.
		return err
	case errors.Is(ctx.Err(), context.Canceled):
		// The caller went away, which isn't the upstream's fault. Keep the
		// error typed so the handler can tell this was a disconnect.
		s.logger.LogAttrs(ctx, slog.LevelDebug, "upstream request cancelled",
			append(attrs, slog.Any(httpx.LogKeyError, err))...)
	default:
		s.logger.LogAttrs(ctx, slog.LevelWarn, "upstream request failed",
			append(attrs, slog.Any(httpx.LogKeyError, err))...)
	}
	return &UpstreamError{Call: call, Status: status, Duration: elapsed, Err: err}
}

// roundTrip performs one call. It returns the response status (0 if no
// response arrived) alongside any error.
func (s *HTTPStore) roundTrip(ctx context.Context, method, path string, body any, want int, out any) (status int, err error) {
	// Everything below that reaches an error message, and so a log line, uses
	// logPath, not path.
	logPath := httpx.Truncate(path, httpx.MaxLoggedPath)
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("upstream %s %s: encode request: %w", method, logPath, err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reqBody)
	if err != nil {
		return 0, fmt.Errorf("upstream %s %s: %w", method, logPath, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("upstream %s %s: %w", method, logPath, httpx.RedactURLError(err))
	}
	respBody := io.LimitReader(resp.Body, maxResponseBytes)
	defer func() {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, respBody)
		resp.Body.Close()
	}()

	if resp.StatusCode != want {
		// Only a JSON 404 means "no such item"; a plain-text 404 means the
		// route itself is missing, i.e. the base URL is misconfigured.
		if resp.StatusCode == http.StatusNotFound && isJSON(resp.Header) {
			return resp.StatusCode, ErrNotFound
		}
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(respBody).Decode(&e)
		return resp.StatusCode, fmt.Errorf("upstream %s %s: unexpected status %d: %q", method, logPath, resp.StatusCode, httpx.Truncate(e.Error, maxLoggedUpstreamMessage))
	}

	if out == nil {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(respBody).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("upstream %s %s: decode response: %w", method, logPath, err)
	}
	return resp.StatusCode, nil
}

func isJSON(h http.Header) bool {
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	return mt == "application/json"
}
