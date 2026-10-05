package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
)

// The units Go's benchmarks report, as benchfmt normalizes them.
const (
	unitTime   = "sec/op"
	unitBytes  = "B/op"
	unitAllocs = "allocs/op"
)

// How large a statistically significant change must be to be reported as a
// regression or an improvement. Allocation counts are exact, so any change
// counts; bytes and time vary a little from run to run even when nothing
// changed.
var noticeable = map[string]float64{unitAllocs: 0, unitBytes: 0.10, unitTime: 0.15}

// benchKey identifies one measurement: a benchmark in a package, in a unit.
type benchKey struct{ pkg, name, unit string }

// benchRuns is every measurement in a file, with the order the benchmarks
// first appeared in, so the report lists them as the code does.
type benchRuns struct {
	values map[benchKey][]float64
	order  []benchKey
}

var procsSuffix = regexp.MustCompile(`-\d+$`)

// readBenchFile reads Go benchmark output. The package path is shortened to
// what follows the module path, and the -N GOMAXPROCS suffix dropped, so the
// two sides match even if they ran with different CPU counts.
func readBenchFile(path string) (benchRuns, error) {
	runs := benchRuns{values: map[benchKey][]float64{}}
	f, err := os.Open(path)
	if err != nil {
		return runs, err
	}
	defer f.Close()
	rd := benchfmt.NewReader(f, path)
	for rd.Scan() {
		res, ok := rd.Result().(*benchfmt.Result)
		if !ok {
			continue // unit metadata, or a line benchfmt couldn't parse
		}
		pkg := res.GetConfig("pkg")
		if i := strings.Index(pkg, "/golang-vibe-starter/"); i >= 0 {
			pkg = pkg[i+len("/golang-vibe-starter/"):]
		}
		name := procsSuffix.ReplaceAllString(res.Name.String(), "")
		for _, v := range res.Values {
			k := benchKey{pkg, name, v.Unit}
			if _, seen := runs.values[k]; !seen {
				runs.order = append(runs.order, k)
			}
			runs.values[k] = append(runs.values[k], v.Value)
		}
	}
	if err := rd.Err(); err != nil {
		return runs, fmt.Errorf("%s: %w", path, err)
	}
	if len(runs.order) == 0 {
		return runs, fmt.Errorf("%s: no benchmark results", path)
	}
	return runs, nil
}

// benchDelta is one measurement compared across the two sides.
type benchDelta struct {
	benchKey
	base, head  float64 // medians
	change      float64 // (head-base)/base; +Inf from zero
	significant bool    // benchstat's test says the samples differ
	delta       string  // as benchstat prints it: "+3.20%", or "~" if not significant
	n           int     // runs on the smaller side
}

// verdict sorts a change into one of the report's sections.
type verdict int

const (
	unchanged verdict = iota
	improved
	regressed
)

func (d benchDelta) verdict() verdict {
	if !d.significant || math.Abs(d.change) <= noticeable[d.unit] {
		return unchanged
	}
	if d.head > d.base {
		return regressed
	}
	return improved
}

// blocking reports whether the change fails the pull request: more
// allocations per operation, the one benchmark measure that is exact.
func (d benchDelta) blocking() bool {
	return d.unit == unitAllocs && d.verdict() == regressed
}

type benchComparison struct {
	deltas         []benchDelta
	added, removed []string // benchmarks on one side only, as pkg.Name
}

// compareBench compares every measurement the two sides share, as benchstat
// does: medians, and a Mann-Whitney U test for whether they differ.
func compareBench(base, head benchRuns) benchComparison {
	var c benchComparison
	thresholds := benchmath.DefaultThresholds
	seen := map[string]bool{}
	for _, k := range head.order {
		hv := head.values[k]
		bv, ok := base.values[k]
		if !ok {
			if id := k.pkg + "." + k.name; !seen[id] {
				seen[id] = true
				c.added = append(c.added, id)
			}
			continue
		}
		bs, hs := benchmath.NewSample(bv, &thresholds), benchmath.NewSample(hv, &thresholds)
		bsum := benchmath.AssumeNothing.Summary(bs, 0.95)
		hsum := benchmath.AssumeNothing.Summary(hs, 0.95)
		cmp := benchmath.AssumeNothing.Compare(bs, hs)
		d := benchDelta{
			benchKey:    k,
			base:        bsum.Center,
			head:        hsum.Center,
			significant: cmp.P < cmp.Alpha,
			delta:       cmp.FormatDelta(bsum.Center, hsum.Center),
			n:           min(len(bv), len(hv)),
		}
		switch {
		case d.base != 0:
			d.change = (d.head - d.base) / d.base
		case d.head != 0:
			d.change = math.Inf(1)
			if d.significant {
				d.delta = "from 0" // benchstat prints "?": no percentage of zero
			}
		}
		c.deltas = append(c.deltas, d)
	}
	for _, k := range base.order {
		if _, ok := head.values[k]; !ok {
			if id := k.pkg + "." + k.name; !seen[id] {
				seen[id] = true
				c.removed = append(c.removed, id)
			}
		}
	}
	return c
}

// formatValue prints a measurement in its unit's usual form.
func formatValue(unit string, v float64) string {
	switch unit {
	case unitTime:
		switch {
		case v >= 1:
			return fmt.Sprintf("%.2fs", v)
		case v >= 1e-3:
			return fmt.Sprintf("%.2fms", v*1e3)
		case v >= 1e-6:
			return fmt.Sprintf("%.2fµs", v*1e6)
		default:
			return fmt.Sprintf("%.1fns", v*1e9)
		}
	case unitBytes:
		if v >= 10*1024 {
			return fmt.Sprintf("%.1fKiB", v/1024)
		}
		return fmt.Sprintf("%.0fB", v)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}
