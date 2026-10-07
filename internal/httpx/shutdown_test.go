package httpx_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

// shutdownResult is what serveUntilShutdown saw.
type shutdownResult struct {
	status    int // the client's response, if it got one
	body      string
	clientErr error // why it got none
	serveErr  error // what Serve returned
	logs      *logtest.Buffer
}

// serveUntilShutdown runs handler with a short shutdown timeout, sends it
// one request, starts shutting down while the request is in flight, and
// returns what the client got, what Serve returned, and the logs.
func serveUntilShutdown(t *testing.T, handler http.Handler) shutdownResult {
	t.Helper()
	logs := &logtest.Buffer{}
	logger := slog.New(logs.Handler(slog.LevelDebug))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- httpx.Serve(ctx, logger, "127.0.0.1:0", 200*time.Millisecond, httpx.Middleware(logger, handler))
	}()
	addr := logs.WaitFor(t, "server started", 5*time.Second)["addr"].(string)
	type result struct {
		status int
		body   string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/items")
		if err != nil {
			got <- result{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		got <- result{status: resp.StatusCode, body: string(b)}
	}()
	time.Sleep(100 * time.Millisecond) // the request is in flight
	cancel()
	r := <-got
	return shutdownResult{status: r.status, body: r.body, clientErr: r.err, serveErr: <-done, logs: logs}
}

func msgs(t *testing.T, logs *logtest.Buffer) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, r := range logs.Records(t) {
		out[r["msg"].(string)] = r
	}
	return out
}

// A request still running when shutdown runs out of time is stopped, and
// answers: 503, shutting_down, logged, and the shutdown is clean.
func TestShutdownStopsRequestsInFlight(t *testing.T) {
	res := serveUntilShutdown(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // waiting on something, as an upstream call would
		httpx.ServerError(slog.New(slog.DiscardHandler), w, r, r.Context().Err())
	}))
	var e struct{ Code string }
	if res.clientErr != nil || res.status != http.StatusServiceUnavailable || json.Unmarshal([]byte(res.body), &e) != nil || e.Code != "shutting_down" {
		t.Errorf("client got %d %q (%v), want 503 shutting_down", res.status, res.body, res.clientErr)
	}
	if res.serveErr != nil {
		t.Errorf("Serve = %v, want a clean shutdown: every request answered", res.serveErr)
	}
	m := msgs(t, res.logs)
	if r := m["shutdown timed out; stopping requests in flight"]; r == nil || r["in_flight"] != float64(1) {
		t.Errorf("shutdown timed out line: %v", r)
	}
	if r := m["response sent"]; r == nil || r["status"] != float64(503) {
		t.Errorf("response sent: %v", r)
	}
}

// A request that ignores being stopped is cut off after the grace period,
// and Serve says so; it's still logged when its handler returns, if the
// process lives that long.
func TestShutdownCutsOffWhatWontStop(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	res := serveUntilShutdown(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	if res.clientErr == nil {
		t.Error("the client got a response from a handler that never returned")
	}
	if res.serveErr == nil || !strings.Contains(res.serveErr.Error(), "1 requests still in flight") {
		t.Errorf("Serve = %v, want one saying a request was cut off", res.serveErr)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("shutdown took %v: the grace period is 2s", d)
	}
}
