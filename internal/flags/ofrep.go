// Package flags evaluates OpenFeature feature flags by calling a flagd
// sidecar over OFREP, the OpenFeature Remote Evaluation Protocol (plain
// HTTP/JSON). Using OFREP rather than the gRPC provider keeps the module free
// of third-party dependencies.
package flags

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// maxResponseBytes caps how much of a flagd response is read.
const maxResponseBytes = 1 << 20 // 1 MiB

// OFREP evaluates flags against an OFREP endpoint such as flagd's (port 8016).
type OFREP struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// NewOFREP returns an evaluator that sends requests to baseURL (for example
// "http://flagd:8016") using client. The client should have a short timeout,
// because evaluation happens on the request path.
func NewOFREP(baseURL string, client *http.Client, logger *slog.Logger) *OFREP {
	return &OFREP{baseURL: strings.TrimRight(baseURL, "/"), client: client, logger: logger}
}

// Bool returns the value of the boolean flag key. If the flag can't be
// evaluated (flagd unreachable, flag missing, wrong type), it logs a warning
// and returns def: a flag problem never fails a request.
func (c *OFREP) Bool(ctx context.Context, key string, def bool) bool {
	v, err := c.evalBool(ctx, key)
	if err != nil {
		c.logger.Warn("flag evaluation failed, using default", "flag", key, "default", def, "error", err)
		return def
	}
	return v
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
		return false, fmt.Errorf("ofrep %s: %w", path, err)
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
		return false, fmt.Errorf("ofrep %s: unexpected status %d: %s %q", path, resp.StatusCode, e.ErrorCode, e.ErrorDetails)
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
