package profiling

import (
	"context"
	"log/slog"
	"math"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// LogRuntime logs, once, what the Go runtime runs with: the Go version,
// GOMAXPROCS, and the memory limit (0 when there's none) and where it came
// from (LimitMemory's answer), the first things to check when memory or CPU
// misbehave, and how often LogRuntimeStats reports.
func LogRuntime(logger *slog.Logger, statsInterval time.Duration, memoryLimitSource string) {
	limit := debug.SetMemoryLimit(-1) // -1 reads it without changing it
	if limit == math.MaxInt64 {
		limit = 0 // the default: no limit
	}
	logger.Info("using runtime",
		logx.KeyGoVersion, runtime.Version(),
		logx.KeyGOMAXPROCS, runtime.GOMAXPROCS(0),
		logx.KeyMemoryLimitBytes, limit,
		logx.KeyMemoryLimitSource, memoryLimitSource,
		logx.KeyStatsInterval, statsInterval.String())
}

// The runtime/metrics a "runtime stats" line reports.
const (
	metricHeap       = "/memory/classes/heap/objects:bytes" // live objects, and those not yet swept
	metricTotal      = "/memory/classes/total:bytes"        // all the runtime has mapped: close to its share of RSS
	metricGoroutines = "/sched/goroutines:goroutines"
	metricGCCycles   = "/gc/cycles/total:gc-cycles"
)

// LogRuntimeStats logs "runtime stats" every interval until ctx is done: the
// heap, all the memory the runtime holds, the goroutines, and the GC cycles
// so far. Where the logs are all there is, as in a production without a
// metrics backend, these lines are the trend: a leak is a heap or a goroutine
// count whose low points keep rising, and memory that grows while the heap
// doesn't is the runtime holding memory, not the program. An interval of
// zero logs nothing.
func LogRuntimeStats(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	if interval <= 0 {
		return
	}
	samples := []metrics.Sample{{Name: metricHeap}, {Name: metricTotal}, {Name: metricGoroutines}, {Name: metricGCCycles}}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		logRuntimeStats(logger, samples)
	}
}

// logRuntimeStats reads samples and logs them as one "runtime stats" line.
func logRuntimeStats(logger *slog.Logger, samples []metrics.Sample) {
	metrics.Read(samples)
	logger.Info("runtime stats",
		logx.KeyHeapBytes, value(samples[0]),
		logx.KeyRuntimeMemoryBytes, value(samples[1]),
		logx.KeyGoroutines, value(samples[2]),
		logx.KeyGCCycles, value(samples[3]))
}

// value is a sample's value as a number, or 0 if this runtime doesn't have
// the metric.
func value(s metrics.Sample) uint64 {
	if s.Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s.Value.Uint64()
}
