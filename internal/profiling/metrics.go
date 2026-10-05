package profiling

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// MetricsPath is where the profiling listener serves the runtime metrics.
const MetricsPath = "/metrics"

// startTime is when the process started, near enough: when this package was
// initialized.
var startTime = time.Now()

// MetricsHandler serves the Go runtime's own measurements, and the process's
// CPU time and memory, in Prometheus's text format, so Prometheus (or curl)
// can read them without a client library.
//
// Every scalar metric in runtime/metrics is included, named the way the
// Prometheus Go client names them: "/gc/heap/allocs:bytes" becomes
// go_gc_heap_allocs_bytes_total (a counter, so _total). Histograms, such as
// GC pause times, are left out. Their buckets would dwarf everything else,
// and the counters say most of it: GC cycles, CPU spent in GC, heap size.
// The process metrics follow the Prometheus conventions:
// process_cpu_seconds_total, process_resident_memory_bytes (Linux only, which
// is what the container runs) and process_start_time_seconds.
//
// extra writes more metrics after these, such as the service's request
// metrics (httpmetrics.Recorder.WritePrometheus).
func MetricsHandler(extra ...func(io.Writer)) http.Handler {
	descs := scalarMetrics()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		samples := make([]metrics.Sample, len(descs))
		for i, d := range descs {
			samples[i].Name = d.Name
		}
		metrics.Read(samples)

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		bw := bufio.NewWriter(w)
		for i, d := range descs {
			var v float64
			switch samples[i].Value.Kind() {
			case metrics.KindUint64:
				v = float64(samples[i].Value.Uint64())
			case metrics.KindFloat64:
				v = samples[i].Value.Float64()
			default:
				continue // not supported by this Go version after all
			}
			kind := "gauge"
			if d.Cumulative {
				kind = "counter"
			}
			writeMetric(bw, promName(d.Name, d.Cumulative), kind, d.Description, v)
		}
		writeMetric(bw, "go_info", "gauge", "The Go version the binary was built with, as a label.", 1, "version", runtime.Version())
		writeProcessMetrics(bw)
		for _, write := range extra {
			write(bw)
		}
		_ = bw.Flush()
	})
}

// scalarMetrics returns the runtime metrics with a single value.
func scalarMetrics() []metrics.Description {
	var out []metrics.Description
	for _, d := range metrics.All() {
		if d.Kind == metrics.KindUint64 || d.Kind == metrics.KindFloat64 {
			out = append(out, d)
		}
	}
	return out
}

// promName turns a runtime/metrics name into a Prometheus one, the way the
// Prometheus Go client does: "/sched/goroutines:goroutines" becomes
// go_sched_goroutines_goroutines, and a cumulative metric gets _total.
func promName(name string, cumulative bool) string {
	path, unit, _ := strings.Cut(strings.TrimPrefix(name, "/"), ":")
	n := "go_" + sanitize(path) + "_" + sanitize(unit)
	if cumulative && !strings.HasSuffix(n, "_total") {
		n += "_total"
	}
	return n
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// writeProcessMetrics writes the process's CPU time and start time, and on
// Linux its resident memory.
func writeProcessMetrics(w *bufio.Writer) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		cpu := time.Duration(ru.Utime.Nano() + ru.Stime.Nano()).Seconds()
		writeMetric(w, "process_cpu_seconds_total", "counter", "Total user and system CPU time spent, in seconds.", cpu)
	}
	if rss, ok := residentBytes(); ok {
		writeMetric(w, "process_resident_memory_bytes", "gauge", "Resident memory size, in bytes.", rss)
	}
	writeMetric(w, "process_start_time_seconds", "gauge", "When the process started, in seconds since the Unix epoch.", float64(startTime.UnixNano())/1e9)
}

// residentBytes reads the process's resident set size from /proc, which only
// Linux has.
func residentBytes() (float64, bool) {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, false
	}
	return pages * float64(os.Getpagesize()), true
}

// writeMetric writes one metric with its HELP and TYPE lines, and an optional
// label as a name and a value.
func writeMetric(w *bufio.Writer, name, kind, help string, v float64, label ...string) {
	help = strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(help)
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	value := strconv.FormatFloat(v, 'g', -1, 64)
	if math.IsInf(v, 1) {
		value = "+Inf"
	}
	if len(label) == 2 {
		fmt.Fprintf(w, "%s{%s=%q} %s\n", name, label[0], label[1], value)
		return
	}
	fmt.Fprintf(w, "%s %s\n", name, value)
}
