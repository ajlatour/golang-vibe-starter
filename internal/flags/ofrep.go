// Package flags evaluates OpenFeature feature flags by calling a flagd
// sidecar over OFREP, the OpenFeature Remote Evaluation Protocol (plain
// HTTP/JSON). Using OFREP rather than the gRPC provider keeps the module free
// of third-party dependencies.
package flags

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// maxResponseBytes caps how much of a flagd response is read.
const maxResponseBytes = 1 << 20 // 1 MiB

// OFREP evaluates flags against an OFREP endpoint such as flagd's (port 8016).
type OFREP struct {
	baseURL  string
	client   *http.Client
	logger   *slog.Logger
	defaults Defaults

	mu       sync.Mutex
	outage   bool      // flagd is currently failing to answer
	since    time.Time // when the current outage was first seen
	fallback int       // evaluations answered from defaults during the outage
	reported map[string]bool
}

// NewOFREP returns an evaluator that sends requests to baseURL (for example
// "http://flagd:8016") using client. The client should have a short timeout,
// because evaluation happens on the request path. defaults supplies the value
// for any flag flagd can't evaluate.
func NewOFREP(baseURL string, client *http.Client, logger *slog.Logger, defaults Defaults) *OFREP {
	return &OFREP{baseURL: strings.TrimRight(baseURL, "/"), client: client, logger: logger, defaults: defaults}
}

// Bool returns the value of the boolean flag key. If the flag can't be
// evaluated (flagd unreachable, flag missing, wrong type), it returns the
// flag's configured default: a flag problem never fails a request. A flag with
// no configured default is a bug in the defaults file and is treated as false.
//
// Failures are logged so they're visible without flooding the logs:
//   - flagd not answering (network error, 5xx) logs one warning when the
//     outage starts and one info line, with a count of the evaluations that
//     fell back to defaults, when it ends. Each failed evaluation in between
//     logs at debug.
//   - A problem with one flag (unknown, wrong type, bad response) logs one
//     warning for that flag until it evaluates successfully again.
func (c *OFREP) Bool(ctx context.Context, key string) bool {
	v, err := c.evalBool(ctx, key)
	var unavailable *unavailableError
	switch {
	case err == nil:
		c.reachable(ctx)
		c.clearReported("flag:" + key)
		return v
	case errors.As(err, &unavailable):
		c.unavailable(ctx, key, err)
	default:
		c.reachable(ctx) // flagd answered; only this flag is the problem
		if c.firstReport("flag:" + key) {
			c.logger.WarnContext(ctx, "flag evaluation failed, using default", "flag", key, "default", c.defaults[key], httpx.LogKeyError, err)
		} else {
			c.logger.DebugContext(ctx, "flag evaluation failed, using default", "flag", key, "default", c.defaults[key], httpx.LogKeyError, err)
		}
	}

	def, ok := c.defaults[key]
	if !ok && c.firstReport("nodefault:"+key) {
		c.logger.ErrorContext(ctx, "flag has no configured default, using false", "flag", key)
	}
	return def
}

// unavailableError marks a failure to get an answer from flagd at all, as
// opposed to flagd answering that this flag can't be evaluated.
type unavailableError struct{ err error }

func (e *unavailableError) Error() string { return e.err.Error() }
func (e *unavailableError) Unwrap() error { return e.err }

// unavailable records a failed evaluation caused by flagd not answering.
func (c *OFREP) unavailable(ctx context.Context, key string, err error) {
	c.mu.Lock()
	first := !c.outage
	if first {
		c.outage, c.since, c.fallback = true, time.Now(), 0
	}
	c.fallback++
	c.mu.Unlock()

	if first {
		c.logger.WarnContext(ctx, "flag service unavailable, using default flag values", "flag", key, httpx.LogKeyError, err)
		return
	}
	c.logger.DebugContext(ctx, "flag evaluation failed, using default", "flag", key, httpx.LogKeyError, err)
}

// reachable records that flagd answered, ending any outage.
func (c *OFREP) reachable(ctx context.Context) {
	c.mu.Lock()
	if !c.outage {
		c.mu.Unlock()
		return
	}
	since, fallbacks := c.since, c.fallback
	c.outage, c.fallback = false, 0
	c.mu.Unlock()

	c.logger.InfoContext(ctx, "flag service recovered",
		"fallbacks", fallbacks,
		httpx.DurationMS(time.Since(since)),
	)
}

// firstReport reports whether this is the first time id has been reported
// since it was last cleared, and marks it reported.
func (c *OFREP) firstReport(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reported[id] {
		return false
	}
	if c.reported == nil {
		c.reported = map[string]bool{}
	}
	c.reported[id] = true
	return true
}

func (c *OFREP) clearReported(id string) {
	c.mu.Lock()
	delete(c.reported, id)
	c.mu.Unlock()
}

func (c *OFREP) evalBool(ctx context.Context, key string) (bool, error) {
	path := "/ofrep/v1/evaluate/flags/" + url.PathEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader([]byte(`{"context":{}}`)))
	if err != nil {
		return false, fmt.Errorf("ofrep %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return false, &unavailableError{fmt.Errorf("ofrep %s: %w", path, err)}
	}
	body := io.LimitReader(resp.Body, maxResponseBytes)
	defer func() {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		var e struct {
			ErrorCode    string `json:"errorCode"`
			ErrorDetails string `json:"errorDetails"`
		}
		_ = json.NewDecoder(body).Decode(&e)
		err := fmt.Errorf("ofrep %s: unexpected status %d: %s %q", path, resp.StatusCode, e.ErrorCode, e.ErrorDetails)
		if resp.StatusCode >= 500 {
			return false, &unavailableError{err}
		}
		return false, err
	}

	var out struct {
		Value any `json:"value"`
	}
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		return false, fmt.Errorf("ofrep %s: decode response: %w", path, err)
	}
	v, ok := out.Value.(bool)
	if !ok {
		return false, fmt.Errorf("ofrep %s: value is %T, want bool", path, out.Value)
	}
	return v, nil
}
