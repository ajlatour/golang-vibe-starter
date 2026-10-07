package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

func probe(t *testing.T, rd *httpx.Readiness) (int, map[string]string) {
	t.Helper()
	rec := httptest.NewRecorder()
	rd.ServeHTTP(rec, httptest.NewRequest("GET", httpx.ReadyzPath, nil))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return rec.Code, body
}

func TestReadiness(t *testing.T) {
	var logs logtest.Buffer
	var calls atomic.Int32
	var failing atomic.Bool
	check := httpx.Check{Name: "upstream", Fn: func(ctx context.Context) error {
		calls.Add(1)
		if _, ok := ctx.Deadline(); !ok {
			t.Error("the check's context has no deadline")
		}
		if failing.Load() {
			return errors.New("connection refused")
		}
		return nil
	}}
	rd := httpx.NewReadiness(logs.Logger(slog.LevelDebug), 50*time.Millisecond, time.Second, check)

	if code, body := probe(t, rd); code != 200 || body["status"] != "ready" {
		t.Fatalf("healthy: %d %v", code, body)
	}
	// Probes within the TTL reuse the result.
	for range 10 {
		probe(t, rd)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("11 probes within the TTL ran the check %d times, want 1", n)
	}

	failing.Store(true)
	time.Sleep(60 * time.Millisecond)
	if code, body := probe(t, rd); code != 503 || body["reason"] != "upstream unavailable" {
		t.Errorf("failing check: %d %v, want 503 and upstream unavailable", code, body)
	}
	failing.Store(false)
	time.Sleep(60 * time.Millisecond)
	if code, _ := probe(t, rd); code != 200 {
		t.Errorf("recovered: %d, want 200", code)
	}

	rd.Stopping()
	rd.Stopping()
	if code, body := probe(t, rd); code != 503 || body["reason"] != "shutting down" {
		t.Errorf("shutting down: %d %v", code, body)
	}

	var msgs []string
	for _, r := range logs.Records(t) {
		msgs = append(msgs, r["msg"].(string)+": "+str(r["reason"]))
	}
	want := []string{"not ready: upstream unavailable", "ready again: ", "not ready: shutting down"}
	if len(msgs) != len(want) {
		t.Fatalf("logs = %q, want one line per change: %q", msgs, want)
	}
	for i := range want {
		if msgs[i] != want[i] {
			t.Errorf("log %d = %q, want %q", i, msgs[i], want[i])
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// TestShutdownDelay checks the server keeps serving for the delay after
// shutdown begins, with the OnShutdown hooks already run.
func TestShutdownDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var stopped atomic.Bool
	var logs logtest.Buffer
	errc := make(chan error, 1)
	go func() {
		errc <- httpx.Serve(ctx, logs.Logger(slog.LevelInfo), "127.0.0.1:0", time.Second,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stopped.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}),
			httpx.OnShutdown(func() { stopped.Store(true) }), httpx.ShutdownDelay(300*time.Millisecond))
	}()
	addr := logs.WaitFor(t, "server started", 5*time.Second)["addr"].(string)

	start := time.Now()
	cancel()
	time.Sleep(100 * time.Millisecond)
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("during the delay the server refused a request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("during the delay: %d, want the hook to have run (503)", resp.StatusCode)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 300*time.Millisecond {
		t.Errorf("shutdown took %s, want at least the 300ms delay", took)
	}
}
