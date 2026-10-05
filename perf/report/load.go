package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// loadSide is one version's k6 results, averaged over its runs.
type loadSide struct {
	runs      int
	median    float64 // ms
	p99       float64 // ms
	errorRate float64
	requests  int

	// What the runs cost the API, from perf/resources.sh, over the runs that
	// recorded it: the container's average CPU and peak memory from docker
	// stats, and from the API's own metrics, its CPU time, allocations and
	// GC cycles, with the requests of those runs to divide them by.
	statsRuns     int
	cpuPercent    float64
	memoryPeakMiB float64
	metricRuns    int
	cpuSeconds    float64
	allocBytes    float64
	gcCycles      float64
	goroutinesMax float64
	metricReqs    float64
}

func (s loadSide) cpuPerRequest() float64    { return s.cpuSeconds / s.metricReqs }      // seconds
func (s loadSide) allocsPerRequest() float64 { return s.allocBytes / s.metricReqs }      // bytes
func (s loadSide) gcPer1000() float64        { return s.gcCycles * 1000 / s.metricReqs } // cycles

type loadComparison struct {
	base, head loadSide
}

// A p99 increase that is reported as a regression: more than this fraction,
// and more than minP99Change in absolute terms, so a jump from 2ms to 2.6ms
// on a shared runner doesn't raise an alarm.
const (
	p99Noticeable = 0.25
	minP99Change  = 1.0 // ms
)

func (c loadComparison) p99Regressed() bool {
	return c.head.p99 > c.base.p99*(1+p99Noticeable) && c.head.p99-c.base.p99 > minP99Change
}

// Resource increases reported as regressions: CPU per request up by more
// than this fraction, or the container's peak memory up by more than this
// fraction and more than minMemoryChange.
const (
	cpuNoticeable    = 0.25
	memoryNoticeable = 0.25
	minMemoryChange  = 10.0 // MiB
)

func (c loadComparison) hasStats() bool   { return c.base.statsRuns > 0 && c.head.statsRuns > 0 }
func (c loadComparison) hasMetrics() bool { return c.base.metricRuns > 0 && c.head.metricRuns > 0 }

func (c loadComparison) cpuRegressed() bool {
	return c.hasMetrics() && c.head.cpuPerRequest() > c.base.cpuPerRequest()*(1+cpuNoticeable)
}

func (c loadComparison) memoryRegressed() bool {
	return c.hasStats() && c.head.memoryPeakMiB > c.base.memoryPeakMiB*(1+memoryNoticeable) &&
		c.head.memoryPeakMiB-c.base.memoryPeakMiB > minMemoryChange
}

// k6Summary is the part of a k6 --summary-export file the report reads.
type k6Summary struct {
	Metrics map[string]map[string]any `json:"metrics"`
}

// metric returns a field of the first of the named metrics the summary has,
// such as the steady phase's latency before the whole run's.
func (s k6Summary) metric(field string, names ...string) (float64, bool) {
	for _, n := range names {
		if m, ok := s.Metrics[n]; ok {
			if v, ok := m[field].(float64); ok {
				return v, true
			}
		}
	}
	return 0, false
}

// readLoad reads the base-<n>.json and head-<n>.json summaries in dir.
func readLoad(dir string) (loadComparison, error) {
	var c loadComparison
	var err error
	if c.base, err = readLoadSide(dir, "base"); err != nil {
		return c, err
	}
	c.head, err = readLoadSide(dir, "head")
	return c, err
}

// readLoadSide reads the <prefix>-<n>.json summaries in dir, and what
// perf/resources.sh recorded beside them, averaged over the runs.
func readLoadSide(dir, prefix string) (loadSide, error) {
	var side loadSide
	files, err := filepath.Glob(filepath.Join(dir, prefix+"-*.json"))
	if err != nil {
		return side, err
	}
	if len(files) == 0 {
		return side, fmt.Errorf("%s: no %s-<n>.json k6 summaries", dir, prefix)
	}
	slices.Sort(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return side, err
		}
		var s k6Summary
		if err := json.Unmarshal(b, &s); err != nil {
			return side, fmt.Errorf("%s: %w", f, err)
		}
		// The steady phase, when the profile has one, so the warm-up is
		// left out; otherwise the whole run.
		durations := []string{"http_req_duration{phase:steady}", "http_req_duration"}
		med, ok1 := s.metric("med", durations...)
		p99, ok2 := s.metric("p(99)", durations...)
		rate, ok3 := s.metric("value", "http_req_failed{phase:steady}", "http_req_failed")
		reqs, ok4 := s.metric("count", "http_reqs")
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return side, fmt.Errorf("%s: missing http_req_duration (med, p(99)), http_req_failed or http_reqs", f)
		}
		side.runs++
		side.median += med
		side.p99 += p99
		side.errorRate += rate
		side.requests += int(reqs)

		res := readResources(f)
		if res.hasStats {
			side.statsRuns++
			side.cpuPercent += res.cpuPercent
			side.memoryPeakMiB = max(side.memoryPeakMiB, res.memoryPeakMiB)
		}
		if res.hasMetrics {
			side.metricRuns++
			side.cpuSeconds += res.cpuSeconds
			side.allocBytes += res.allocBytes
			side.gcCycles += res.gcCycles
			side.goroutinesMax = max(side.goroutinesMax, res.goroutines)
			side.metricReqs += reqs
		}
	}
	n := float64(side.runs)
	side.median /= n
	side.p99 /= n
	side.errorRate /= n
	if side.statsRuns > 0 {
		side.cpuPercent /= float64(side.statsRuns)
	}
	return side, nil
}
