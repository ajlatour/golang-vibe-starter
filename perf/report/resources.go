package main

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// resources is what one load run cost the API, from the files
// perf/resources.sh writes next to the k6 summary. Each part is optional: a
// base from before the sampler existed has none of it.
type resources struct {
	// From docker stats: the API container.
	hasStats      bool
	cpuPercent    float64 // average, 100 = one core
	memoryPeakMiB float64

	// From the API's /metrics, before and after the run.
	hasMetrics bool
	cpuSeconds float64
	allocBytes float64
	gcCycles   float64
	goroutines float64 // at the end
}

// readResources reads the resource files for the run whose k6 summary is at
// summaryPath (base-1.json reads base-1.stats.tsv and so on).
func readResources(summaryPath string) resources {
	var r resources
	stem := strings.TrimSuffix(summaryPath, ".json")
	r.cpuPercent, r.memoryPeakMiB, r.hasStats = apiStats(stem + ".stats.tsv")
	start, ok1 := readProm(stem + ".metrics-start.prom")
	end, ok2 := readProm(stem + ".metrics-end.prom")
	if ok1 && ok2 {
		r.hasMetrics = true
		r.cpuSeconds = end["process_cpu_seconds_total"] - start["process_cpu_seconds_total"]
		r.allocBytes = end["go_gc_heap_allocs_bytes_total"] - start["go_gc_heap_allocs_bytes_total"]
		r.gcCycles = end["go_gc_cycles_total_gc_cycles_total"] - start["go_gc_cycles_total_gc_cycles_total"]
		r.goroutines = end["go_sched_goroutines_goroutines"]
	}
	return r
}

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// apiStats averages the API container's CPU and finds its peak memory in
// docker stats output: lines of name, CPU% and "used / limit" memory.
func apiStats(path string) (cpuAvg, memPeakMiB float64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	var sum float64
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(ansiEscape.ReplaceAllString(sc.Text(), ""), "\t")
		if len(fields) < 3 || !strings.HasPrefix(fields[0], "golang-vibe-starter-api-") {
			continue
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(fields[1], "%"), 64)
		if err != nil {
			continue
		}
		used, _, _ := strings.Cut(fields[2], " / ")
		if mib, ok := parseMiB(used); ok && mib > memPeakMiB {
			memPeakMiB = mib
		}
		sum += cpu
		n++
	}
	if n == 0 {
		return 0, 0, false
	}
	return sum / float64(n), memPeakMiB, true
}

// parseMiB reads a docker stats size such as "14.9MiB" or "1.2GiB" in MiB.
func parseMiB(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	for _, u := range []struct {
		suffix string
		scale  float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"kB", 1000.0 / 1048576}, {"MB", 1e6 / 1048576}, {"GB", 1e9 / 1048576}, {"B", 1.0 / 1048576}} {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(num, 64)
			return v * u.scale, err == nil
		}
	}
	return 0, false
}

// readProm reads the unlabelled samples of a Prometheus text file.
func readProm(path string) (map[string]float64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	out := map[string]float64{}
	for _, line := range strings.Split(string(b), "\n") {
		name, value, ok := strings.Cut(line, " ")
		if !ok || strings.HasPrefix(line, "#") || strings.Contains(name, "{") {
			continue
		}
		if v, err := strconv.ParseFloat(value, 64); err == nil {
			out[name] = v
		}
	}
	return out, len(out) > 0
}
