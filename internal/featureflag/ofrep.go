// Package featureflag evaluates OpenFeature feature flags by calling a flagd
// sidecar over OFREP, the OpenFeature Remote Evaluation Protocol (plain
// HTTP/JSON). Using OFREP rather than the gRPC provider keeps the module free
// of third-party dependencies.
package featureflag

import (
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

	"github.com/ajlatour/golang-vibe-starter/internal/jsoncall"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// maxResponseBytes caps how much of a flagd response is read.
const maxResponseBytes = 1 << 20 // 1 MiB

// OFREP evaluates flags against an OFREP endpoint such as flagd's (port 8016).
type OFREP struct {
	baseURL  string
	client   *http.Client
	logger   *slog.Logger
	defaults Defaults

	outage    *outageTracker // is flagd answering?
	problems  onceSet        // flags that failed, reported once until they recover
	noDefault onceSet        // flags with no configured default, reported once

	ttl   time.Duration // see WithCache; zero caches nothing
	now   func() time.Time
	cache valueCache
}

// Option customizes NewOFREP.
type Option func(*OFREP)

// WithCache keeps each flag's value for ttl after flagd serves it, so a busy
// service asks flagd about a flag about once per ttl rather than on every
// request. It also bounds what an outage costs: while flagd isn't answering,
// it is asked at most once per ttl, and the evaluations in between take their
// defaults at once instead of each waiting for the client's timeout. A flag
// change in flagd therefore takes up to ttl to be seen. The default, zero,
// caches nothing and asks flagd every time.
func WithCache(ttl time.Duration) Option {
	return func(c *OFREP) { c.ttl = ttl }
}

// NewOFREP returns an evaluator that sends requests to baseURL (for example
// "http://flagd:8016") using client. The client should have a short timeout,
// because evaluation happens on the request path. defaults supplies the value
// for any flag flagd can't evaluate.
func NewOFREP(baseURL string, client *http.Client, logger *slog.Logger, defaults Defaults, opts ...Option) *OFREP {
	c := &OFREP{
		baseURL:  strings.TrimRight(baseURL, "/"),
		client:   client,
		logger:   logger,
		defaults: defaults,
		outage:   newOutageTracker(),
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
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
//
// With WithCache, a value flagd served within the TTL is returned without
// asking it again, and during an outage it is asked at most once per TTL.
func (c *OFREP) Bool(ctx context.Context, key string) bool {
	if v, ok := c.cache.get(key, c.now()); ok {
		return v
	}
	err := errNotRetried
	if !c.outage.waiting(c.ttl) {
		var v bool
		if v, err = c.evalBool(ctx, key); err == nil {
			if c.ttl > 0 {
				c.cache.put(key, v, c.now().Add(c.ttl))
			}
			c.answered(ctx)
			c.problems.clear(key)
			return v
		}
	}

	def, hasDefault := c.defaults[key]
	if serviceDown(err) {
		if c.outage.failed() {
			c.logger.WarnContext(ctx, "flag service unavailable, using default flag values",
				logx.KeyFlag, key, logx.KeyError, err)
		} else {
			c.logFallback(ctx, slog.LevelDebug, key, def, err)
		}
	} else {
		c.answered(ctx) // flagd answered; only this flag is the problem
		level := slog.LevelDebug
		if c.problems.first(key) {
			level = slog.LevelWarn
		}
		c.logFallback(ctx, level, key, def, err)
	}

	if !hasDefault && c.noDefault.first(key) {
		c.logger.ErrorContext(ctx, "flag has no configured default, using false", logx.KeyFlag, key)
	}
	return def
}

// logFallback logs that a flag couldn't be evaluated and def was used.
func (c *OFREP) logFallback(ctx context.Context, level slog.Level, key string, def bool, err error) {
	c.logger.Log(ctx, level, "flag evaluation failed, using default",
		logx.KeyFlag, key, logx.KeyDefault, def, logx.KeyError, err)
}

// answered notes that flagd replied, and logs the end of an outage if one was
// in progress.
func (c *OFREP) answered(ctx context.Context) {
	if ended, fallbacks, took := c.outage.answered(); ended {
		c.logger.InfoContext(ctx, "flag service recovered",
			logx.KeyFallbacks, fallbacks,
			logx.DurationMS(took),
		)
	}
}

// ofrepRequest is the OFREP evaluation request. The evaluation context is
// empty for now: flagd's targeting rules have nothing to match on yet.
type ofrepRequest struct {
	Context map[string]any `json:"context"`
}

func (c *OFREP) evalBool(ctx context.Context, key string) (bool, error) {
	path := "/ofrep/v1/evaluate/flags/" + url.PathEscape(key)
	var out struct {
		Value any `json:"value"`
	}
	_, err := jsoncall.Call{
		Client:           c.client,
		Method:           http.MethodPost,
		URL:              c.baseURL + path,
		Label:            "ofrep " + path,
		In:               ofrepRequest{Context: map[string]any{}},
		Out:              &out,
		Want:             http.StatusOK,
		MaxResponseBytes: maxResponseBytes,
		ErrorDetail:      ofrepErrorDetail,
	}.Do(ctx)
	if err != nil {
		return false, err
	}

	v, ok := out.Value.(bool)
	if !ok {
		return false, fmt.Errorf("ofrep %s: value is %T, want bool", path, out.Value)
	}
	return v, nil
}

// ofrepErrorDetail describes an OFREP error response: flagd's error code and
// details, each bounded.
func ofrepErrorDetail(body io.Reader) string {
	var e struct {
		ErrorCode    string `json:"errorCode"`
		ErrorDetails string `json:"errorDetails"`
	}
	_ = json.NewDecoder(body).Decode(&e)
	return fmt.Sprintf("%s %q", logx.Truncate(e.ErrorCode, 64), logx.Truncate(e.ErrorDetails, 200))
}

// errNotRetried is why an evaluation during an outage took its default
// without asking flagd: flagd was tried less than the cache TTL ago.
var errNotRetried = errors.New("flag service unavailable; not asked again yet")

// serviceDown reports whether err means flagd gave no answer at all (it
// couldn't be reached, or it failed with a 5xx, or it did so moments ago), as
// opposed to answering that this one flag can't be evaluated.
func serviceDown(err error) bool {
	var ce *jsoncall.Error
	return err == errNotRetried || errors.As(err, &ce) && ce.NoAnswer()
}

// valueCache holds the flag values flagd served, each until it expires. It is
// safe for concurrent use; the zero value is ready. It holds one entry per
// flag the code evaluates, so it never needs pruning.
type valueCache struct {
	mu   sync.Mutex
	vals map[string]cachedValue
}

type cachedValue struct {
	v     bool
	until time.Time
}

func (vc *valueCache) get(key string, now time.Time) (bool, bool) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	cv, ok := vc.vals[key]
	if !ok || !now.Before(cv.until) {
		return false, false
	}
	return cv.v, true
}

func (vc *valueCache) put(key string, v bool, until time.Time) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	if vc.vals == nil {
		vc.vals = map[string]cachedValue{}
	}
	vc.vals[key] = cachedValue{v, until}
}
