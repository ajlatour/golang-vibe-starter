package profiling

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

func TestLogRuntime(t *testing.T) {
	var logs logtest.Buffer
	LogRuntime(logs.Logger(slog.LevelInfo), 5*time.Minute)
	recs := logs.Records(t)
	if len(recs) != 1 || recs[0]["msg"] != "using runtime" {
		t.Fatalf("logs = %v, want one using runtime line", recs)
	}
	r := recs[0]
	if v, _ := r[logx.KeyGoVersion].(string); len(v) < 3 || v[:2] != "go" {
		t.Errorf("go_version = %v, want the runtime's, go1.x", r[logx.KeyGoVersion])
	}
	if n, _ := r[logx.KeyGOMAXPROCS].(float64); n < 1 {
		t.Errorf("gomaxprocs = %v, want at least 1", r[logx.KeyGOMAXPROCS])
	}
	if _, ok := r[logx.KeyMemoryLimitBytes].(float64); !ok {
		t.Errorf("memory_limit_bytes = %v, want a number (0: no limit)", r[logx.KeyMemoryLimitBytes])
	}
	if r[logx.KeyStatsInterval] != "5m0s" {
		t.Errorf("stats_interval = %v, want 5m0s", r[logx.KeyStatsInterval])
	}
}

// TestLogRuntimeStats checks the periodic line reports what the runtime
// holds, as real numbers, every interval until it's stopped.
func TestLogRuntimeStats(t *testing.T) {
	var logs logtest.Buffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		LogRuntimeStats(ctx, logs.Logger(slog.LevelInfo), 10*time.Millisecond)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for logs.Len() == 0 || len(logs.Records(t)) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("no two runtime stats lines within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	for _, r := range logs.Records(t) {
		if r["msg"] != "runtime stats" {
			t.Fatalf("logged %q, want only runtime stats", r["msg"])
		}
		heap, _ := r[logx.KeyHeapBytes].(float64)
		total, _ := r[logx.KeyRuntimeMemoryBytes].(float64)
		goroutines, _ := r[logx.KeyGoroutines].(float64)
		if heap <= 0 || total < heap || goroutines < 2 {
			t.Errorf("stats = heap %v, runtime memory %v, goroutines %v; want a heap, at least as much memory in all, and this test's goroutines", heap, total, goroutines)
		}
		if _, ok := r[logx.KeyGCCycles].(float64); !ok {
			t.Errorf("gc_cycles = %v, want a number", r[logx.KeyGCCycles])
		}
	}
}

func TestLogRuntimeStatsOff(t *testing.T) {
	var logs logtest.Buffer
	LogRuntimeStats(t.Context(), logs.Logger(slog.LevelInfo), 0) // returns at once
	if logs.Len() != 0 {
		t.Errorf("an interval of 0 logged: %s", logs.String())
	}
}
