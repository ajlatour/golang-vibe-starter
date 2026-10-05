package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchOutput writes Go benchmark output: each benchmark in pkg, run n times,
// with the given time (ns), bytes and allocations per operation. Times are
// jittered by up to 1% so the samples look like real ones.
func benchOutput(t *testing.T, pkg string, n int, benches map[string][3]float64) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "goos: linux\ngoarch: amd64\npkg: github.com/ajlatour/golang-vibe-starter/%s\ncpu: Test CPU\n", pkg)
	for i := range n {
		for name, v := range benches {
			jitter := 1 + float64(i%5)/500
			fmt.Fprintf(&b, "Benchmark%s-4\t1000\t%.1f ns/op\t%.0f B/op\t%.0f allocs/op\n", name, v[0]*jitter, v[1], v[2])
		}
	}
	b.WriteString("PASS\n")
	path := filepath.Join(t.TempDir(), "bench.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// k6Dir writes k6 summaries for each side with the given median and p99 (ms)
// and error rate.
func k6Dir(t *testing.T, base, head [3]float64) string {
	t.Helper()
	dir := t.TempDir()
	for side, v := range map[string][3]float64{"base": base, "head": head} {
		for i := 1; i <= 2; i++ {
			summary := fmt.Sprintf(`{"metrics": {
				"http_req_duration{phase:steady}": {"med": %g, "p(99)": %g},
				"http_req_duration": {"med": 99, "p(99)": 99},
				"http_req_failed": {"value": %g},
				"http_reqs": {"count": 1000}}}`, v[0], v[1], v[2])
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.json", side, i)), []byte(summary), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func render(t *testing.T, opts options) (string, bool) {
	t.Helper()
	var out strings.Builder
	blocking, err := run(&out, opts)
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), blocking
}

func mustContain(t *testing.T, report string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(report, w) {
			t.Errorf("report lacks %q:\n%s", w, report)
		}
	}
}

func TestNoChange(t *testing.T) {
	benches := map[string][3]float64{"API/GET": {40000, 17000, 138}, "Log": {700, 192, 5}}
	report, blocking := render(t, options{
		baseName:  "main",
		benchBase: benchOutput(t, "cmd/api", 8, benches),
		benchHead: benchOutput(t, "cmd/api", 8, benches),
		loadDir:   k6Dir(t, [3]float64{1.8, 4.7, 0}, [3]float64{1.8, 4.8, 0}),
	})
	if blocking {
		t.Error("blocking with nothing changed")
	}
	if !strings.HasPrefix(report, Marker+"\n") {
		t.Error("the report doesn't start with the marker the workflow looks for")
	}
	mustContain(t, report, "✅ No regressions found.", "`API/GET` (cmd/api)", "| p99 latency | 4.70ms | 4.80ms | +2.1% |", "Medians of 8 interleaved runs")
	if strings.Contains(report, "⚠️") || strings.Contains(report, "❌") {
		t.Errorf("warnings with nothing changed:\n%s", report)
	}
}

func TestMoreAllocationsBlock(t *testing.T) {
	base := benchOutput(t, "cmd/api", 8, map[string][3]float64{"API/GET": {40000, 17000, 138}})
	head := benchOutput(t, "cmd/api", 8, map[string][3]float64{"API/GET": {40000, 17000, 139}})

	report, blocking := render(t, options{baseName: "main", benchBase: base, benchHead: head})
	if !blocking {
		t.Error("one more allocation per operation didn't block")
	}
	mustContain(t, report, "❌ **Blocking:** 1 benchmark allocates more", "`perf-ok`", "### More allocations", "| `API/GET` (cmd/api) | allocs/op | 138 | 139 |")

	report, blocking = render(t, options{baseName: "main", benchBase: base, benchHead: head, acceptAllocs: true})
	if blocking {
		t.Error("blocking despite the perf-ok label")
	}
	mustContain(t, report, "☑️ **Accepted:**")
}

func TestSlowerWarnsButDoesntBlock(t *testing.T) {
	report, blocking := render(t, options{
		baseName:  "main",
		benchBase: benchOutput(t, "cmd/api", 8, map[string][3]float64{"API/GET": {40000, 17000, 138}, "Small": {100, 0, 0}}),
		benchHead: benchOutput(t, "cmd/api", 8, map[string][3]float64{"API/GET": {60000, 17000, 138}, "Small": {108, 0, 0}}),
	})
	if blocking {
		t.Error("a slower benchmark blocked; timings must only warn")
	}
	mustContain(t, report, "⚠️ **Possible regression:** 1 benchmark slower or larger", "### Possibly slower or larger", "| `API/GET` (cmd/api) | time/op | 40.12µs | 60.18µs | +50.00% |")
	// 8% slower is significant but under the 15% bar.
	if strings.Contains(report, "| `Small` (cmd/api) | time/op") {
		t.Errorf("an 8%% change was reported as a regression:\n%s", report)
	}
}

func TestImprovementsAndNewBenchmarks(t *testing.T) {
	report, blocking := render(t, options{
		baseName:  "main",
		benchBase: benchOutput(t, "internal/logx", 8, map[string][3]float64{"Log": {700, 192, 5}, "Gone": {1, 0, 0}}),
		benchHead: benchOutput(t, "internal/logx", 8, map[string][3]float64{"Log": {350, 96, 3}, "New": {1, 0, 0}}),
	})
	if blocking {
		t.Error("an improvement blocked")
	}
	mustContain(t, report, "✅ No regressions found.", "🚀 3 measurements faster or leaner.", "### Improvements",
		"New benchmarks, with nothing to compare against yet: `internal/logx.New`.", "Benchmarks only in main: `internal/logx.Gone`.")
}

func TestSamePackageNamesStayApart(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	two := func(a, b float64) string {
		var s strings.Builder
		for range 8 {
			fmt.Fprintf(&s, "pkg: m/golang-vibe-starter/internal/httpx\nBenchmarkMiddleware-4\t1\t100 ns/op\t0 B/op\t%g allocs/op\n", a)
			fmt.Fprintf(&s, "pkg: m/golang-vibe-starter/internal/headerguard\nBenchmarkMiddleware-4\t1\t100 ns/op\t0 B/op\t%g allocs/op\n", b)
		}
		return s.String()
	}
	report, blocking := render(t, options{baseName: "main", benchBase: write("b.txt", two(5, 0)), benchHead: write("h.txt", two(5, 1))})
	if !blocking {
		t.Error("the header guard's new allocation was lost among same-named benchmarks")
	}
	mustContain(t, report, "| `Middleware` (internal/headerguard) | allocs/op | 0 | 1 | from 0 |")
}

func TestLoad(t *testing.T) {
	t.Run("p99 well up warns", func(t *testing.T) {
		report, blocking := render(t, options{baseName: "main", loadDir: k6Dir(t, [3]float64{1.8, 4, 0}, [3]float64{2, 9, 0})})
		if blocking {
			t.Error("a higher p99 blocked; it should only warn")
		}
		mustContain(t, report, "⚠️ **Possible regression:** a higher p99 under load")
	})
	t.Run("small absolute changes don't", func(t *testing.T) {
		report, _ := render(t, options{baseName: "main", loadDir: k6Dir(t, [3]float64{1, 2, 0}, [3]float64{1, 2.8, 0})})
		mustContain(t, report, "✅ No regressions found.")
	})
	t.Run("crossed thresholds block", func(t *testing.T) {
		report, blocking := render(t, options{baseName: "main", loadDir: k6Dir(t, [3]float64{1, 2, 0}, [3]float64{1, 2, 0.2}), loadFailed: true})
		if !blocking {
			t.Error("crossed load thresholds didn't block")
		}
		mustContain(t, report, "❌ **Blocking:** the load test", "| Errors | 0.00% | 20.00% | |")
	})
	t.Run("the steady phase is preferred", func(t *testing.T) {
		report, _ := render(t, options{baseName: "main", loadDir: k6Dir(t, [3]float64{1, 2, 0}, [3]float64{1, 2, 0})})
		if strings.Contains(report, "99.00ms") {
			t.Error("the whole run's latency was used, not the steady phase's")
		}
	})
}

func TestLinks(t *testing.T) {
	report, _ := render(t, options{baseName: "main", loadDir: k6Dir(t, [3]float64{1, 2, 0}, [3]float64{1, 2, 0}),
		docsURL: "https://example.com/docs", detailsURL: "https://example.com/run"})
	mustContain(t, report, "[How to read this](https://example.com/docs)", "[Full results](https://example.com/run)")
}

func TestBadInput(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte("PASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string]options{
		"missing file":    {benchBase: "nope.txt", benchHead: "nope.txt"},
		"no results":      {benchBase: empty, benchHead: empty},
		"no head results": {benchBase: benchOutput(t, "cmd/api", 1, map[string][3]float64{"A": {1, 0, 0}}), benchHead: empty},
		"no k6 summaries": {loadDir: t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := run(&strings.Builder{}, opts); err == nil {
				t.Error("no error")
			}
		})
	}
}

// TestNoBenchmarksAtTheBase covers the first pull request to add benchmarks:
// the base has none, so every one is new and nothing blocks.
func TestNoBenchmarksAtTheBase(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	report, blocking := render(t, options{baseName: "main", benchBase: empty,
		benchHead: benchOutput(t, "cmd/api", 8, map[string][3]float64{"API/GET": {40000, 17000, 138}})})
	if blocking {
		t.Error("new benchmarks blocked")
	}
	mustContain(t, report, "✅ No regressions found.", "New benchmarks, with nothing to compare against yet: `cmd/api.API/GET`.")
}

// withResources adds perf/resources.sh's files to every run in a k6Dir: the
// API container at cpu% and memMiB in docker stats (with another project's
// container and an ANSI clear-screen in the mix, as docker stats writes),
// and metrics where the run cost cpuSec of CPU and allocBytes, for the 1000
// requests k6Dir records.
func withResources(t *testing.T, dir, side string, cpu, memMiB, cpuSec, allocBytes float64) {
	t.Helper()
	for i := 1; i <= 2; i++ {
		stem := filepath.Join(dir, fmt.Sprintf("%s-%d", side, i))
		stats := fmt.Sprintf("\x1b[2J\x1b[Hgolang-vibe-starter-api-1\t%.1f%%\t%.1fMiB / 7.6GiB\n"+
			"other-project-db-1\t99.0%%\t2GiB / 7.6GiB\n"+
			"golang-vibe-starter-api-1\t%.1f%%\t%.1fMiB / 7.6GiB\n", cpu, memMiB-1, cpu, memMiB)
		start := "# TYPE process_cpu_seconds_total counter\nprocess_cpu_seconds_total 10\ngo_gc_heap_allocs_bytes_total 1000\ngo_gc_cycles_total_gc_cycles_total 5\n"
		end := fmt.Sprintf("process_cpu_seconds_total %g\ngo_gc_heap_allocs_bytes_total %g\ngo_gc_cycles_total_gc_cycles_total 7\ngo_sched_goroutines_goroutines 12\ngo_info{version=\"go1.27.0\"} 1\n", 10+cpuSec, 1000+allocBytes)
		for name, content := range map[string]string{".stats.tsv": stats, ".metrics-start.prom": start, ".metrics-end.prom": end} {
			if err := os.WriteFile(stem+name, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestResources(t *testing.T) {
	t.Run("both sides recorded", func(t *testing.T) {
		dir := k6Dir(t, [3]float64{1, 2, 0}, [3]float64{1, 2, 0})
		withResources(t, dir, "base", 10, 15, 0.1, 2048*1000)
		withResources(t, dir, "head", 11, 15.5, 0.1, 2048*1000)
		report, blocking := render(t, options{baseName: "main", loadDir: dir})
		if blocking {
			t.Error("resource numbers blocked")
		}
		mustContain(t, report, "✅ No regressions found.", "### Resources (the API, during the load test)",
			"| CPU per request | 100µs | 100µs | +0.0% |", "| CPU, container average | 10.0% | 11.0% | +10.0% |",
			"| Memory, container peak | 15.0 MiB | 15.5 MiB |", "| Allocated per request | 2048B | 2048B |",
			"| GC cycles per 1,000 requests | 2.00 | 2.00 |", "| Goroutines at the end | 12 | 12 | |")
		if strings.Contains(report, "2048.0 MiB") || strings.Contains(report, "99.0%") {
			t.Errorf("another project's container was counted:\n%s", report)
		}
	})
	t.Run("more CPU per request warns", func(t *testing.T) {
		dir := k6Dir(t, [3]float64{1, 2, 0}, [3]float64{1, 2, 0})
		withResources(t, dir, "base", 10, 15, 0.1, 1000)
		withResources(t, dir, "head", 14, 15, 0.14, 1000)
		report, blocking := render(t, options{baseName: "main", loadDir: dir})
		if blocking {
			t.Error("more CPU blocked; it should only warn")
		}
		mustContain(t, report, "⚠️ **Possible regression:** more CPU per request")
	})
	t.Run("more memory warns", func(t *testing.T) {
		dir := k6Dir(t, [3]float64{1, 2, 0}, [3]float64{1, 2, 0})
		withResources(t, dir, "base", 10, 15, 0.1, 1000)
		withResources(t, dir, "head", 10, 40, 0.1, 1000)
		report, _ := render(t, options{baseName: "main", loadDir: dir})
		mustContain(t, report, "⚠️ **Possible regression:** more memory")
	})
	t.Run("a base without them", func(t *testing.T) {
		dir := k6Dir(t, [3]float64{1, 2, 0}, [3]float64{1, 2, 0})
		withResources(t, dir, "head", 10, 15, 0.1, 1000)
		report, _ := render(t, options{baseName: "main", loadDir: dir})
		if strings.Contains(report, "### Resources") {
			t.Errorf("a resources table with nothing to compare against:\n%s", report)
		}
	})
}
