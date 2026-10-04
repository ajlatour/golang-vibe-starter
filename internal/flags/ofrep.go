// Package flags evaluates OpenFeature feature flags by calling a flagd
// sidecar over OFREP, the OpenFeature Remote Evaluation Protocol (plain
// HTTP/JSON). Using OFREP rather than the gRPC provider keeps the module free
// of third-party dependencies.
package flags

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
}

// NewOFREP returns an evaluator that sends requests to baseURL (for example
// "http://flagd:8016") using client. The client should have a short timeout,
// because evaluation happens on the request path. defaults supplies the value
// for any flag flagd can't evaluate.
func NewOFREP(baseURL string, client *http.Client, logger *slog.Logger, defaults Defaults) *OFREP {
	return &OFREP{
		baseURL:  strings.TrimRight(baseURL, "/"),
		client:   client,
		logger:   logger,
		defaults: defaults,
		outage:   newOutageTracker(),
	}
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
	if err == nil {
		c.answered(ctx)
		c.problems.clear(key)
		return v
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

// serviceDown reports whether err means flagd gave no answer at all (it
// couldn't be reached, or it failed with a 5xx), as opposed to answering that
// this one flag can't be evaluated.
func serviceDown(err error) bool {
	var ce *jsoncall.Error
	return errors.As(err, &ce) && ce.NoAnswer()
}
