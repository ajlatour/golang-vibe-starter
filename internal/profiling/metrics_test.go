package profiling_test

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/profiling"
)

// scrape fetches /metrics from the profiling handler and returns each
// metric's TYPE and value, failing on anything Prometheus couldn't parse.
func scrape(t *testing.T) (types map[string]string, values map[string]float64) {
	t.Helper()
	srv := httptest.NewServer(profiling.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + profiling.MetricsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("status %d, Content-Type %q; want 200 and Prometheus's text format", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	nameRE := `[a-zA-Z_:][a-zA-Z0-9_:]*`
	helpRE := regexp.MustCompile(`^# HELP (` + nameRE + `) .*$`)
	typeRE := regexp.MustCompile(`^# TYPE (` + nameRE + `) (counter|gauge)$`)
	sampleRE := regexp.MustCompile(`^(` + nameRE + `)(\{[a-z_]+="[^"]*"\})? (\S+)$`)
	types, values = map[string]string{}, map[string]float64{}
	helped := map[string]bool{}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if m := helpRE.FindStringSubmatch(line); m != nil {
			if helped[m[1]] {
				t.Errorf("%s is described twice", m[1])
			}
			helped[m[1]] = true
			continue
		}
		if m := typeRE.FindStringSubmatch(line); m != nil {
			types[m[1]] = m[2]
			continue
		}
		m := sampleRE.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("can't parse %q", line)
			continue
		}
		if types[m[1]] == "" {
			t.Errorf("%s has no TYPE line before its sample", m[1])
		}
		v, err := strconv.ParseFloat(strings.Replace(m[3], "+Inf", "Inf", 1), 64)
		if err != nil {
			t.Errorf("%s: value %q isn't a number", m[1], m[3])
		}
		values[m[1]] = v
	}
	return types, values
}

func TestMetrics(t *testing.T) {
	types, values := scrape(t)
	for name, kind := range types {
		if (kind == "counter") != strings.HasSuffix(name, "_total") {
			t.Errorf("%s is a %s; counters, and only counters, end in _total", name, kind)
		}
	}
	// The metrics the dashboards and the pull request report rely on.
	for _, name := range []string{
		"go_sched_goroutines_goroutines",
		"go_memory_classes_heap_objects_bytes",
		"go_memory_classes_total_bytes",
		"go_gc_heap_allocs_bytes_total",
		"go_gc_cycles_total_gc_cycles_total",
		"go_cpu_classes_gc_total_cpu_seconds_total",
		"go_sched_gomaxprocs_threads",
		"process_cpu_seconds_total",
		"process_start_time_seconds",
		"go_info",
	} {
		if _, ok := values[name]; !ok {
			t.Errorf("no %s", name)
		}
	}
	if values["go_sched_goroutines_goroutines"] < 1 || values["go_memory_classes_heap_objects_bytes"] <= 0 {
		t.Error("goroutines or heap read as zero; the values aren't being read")
	}
}

func TestMetricsCountersGrow(t *testing.T) {
	_, before := scrape(t)
	garbage := make([][]byte, 0, 1000)
	for range 1000 {
		garbage = append(garbage, make([]byte, 1024))
	}
	_ = garbage
	_, after := scrape(t)
	if after["go_gc_heap_allocs_bytes_total"] < before["go_gc_heap_allocs_bytes_total"]+1000*1024 {
		t.Errorf("allocating 1 MiB moved go_gc_heap_allocs_bytes_total from %g to %g", before["go_gc_heap_allocs_bytes_total"], after["go_gc_heap_allocs_bytes_total"])
	}
}
