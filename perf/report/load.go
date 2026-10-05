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
}

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
	for _, side := range []struct {
		prefix string
		into   *loadSide
	}{{"base", &c.base}, {"head", &c.head}} {
		files, err := filepath.Glob(filepath.Join(dir, side.prefix+"-*.json"))
		if err != nil {
			return c, err
		}
		if len(files) == 0 {
			return c, fmt.Errorf("%s: no %s-<n>.json k6 summaries", dir, side.prefix)
		}
		slices.Sort(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return c, err
			}
			var s k6Summary
			if err := json.Unmarshal(b, &s); err != nil {
				return c, fmt.Errorf("%s: %w", f, err)
			}
			// The steady phase, when the profile has one, so the warm-up is
			// left out; otherwise the whole run.
			durations := []string{"http_req_duration{phase:steady}", "http_req_duration"}
			med, ok1 := s.metric("med", durations...)
			p99, ok2 := s.metric("p(99)", durations...)
			rate, ok3 := s.metric("value", "http_req_failed{phase:steady}", "http_req_failed")
			reqs, ok4 := s.metric("count", "http_reqs")
			if !ok1 || !ok2 || !ok3 || !ok4 {
				return c, fmt.Errorf("%s: missing http_req_duration (med, p(99)), http_req_failed or http_reqs", f)
			}
			side.into.runs++
			side.into.median += med
			side.into.p99 += p99
			side.into.errorRate += rate
			side.into.requests += int(reqs)
		}
		n := float64(side.into.runs)
		side.into.median /= n
		side.into.p99 /= n
		side.into.errorRate /= n
	}
	return c, nil
}
