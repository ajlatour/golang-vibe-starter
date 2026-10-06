package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strings"
)

// Marker starts every report, so the workflow can find its own comment on a
// pull request and edit it rather than add another.
const Marker = "<!-- perf-report -->"

// AcceptLabel is the pull request label that accepts an allocation increase.
const AcceptLabel = "perf-ok"

type report struct {
	baseName     string
	detailsURL   string
	docsURL      string
	acceptAllocs bool
	loadFailed   bool
	bench        benchComparison
	load         *loadComparison
}

func (r report) allocIncreases() []benchDelta {
	var out []benchDelta
	for _, d := range r.bench.deltas {
		if d.blocking() {
			out = append(out, d)
		}
	}
	return out
}

// changes returns the changes with the given verdict, leaving out the
// blocking ones, which have their own section.
func (r report) changes(v verdict) []benchDelta {
	var out []benchDelta
	for _, d := range r.bench.deltas {
		if d.verdict() == v && !d.blocking() {
			out = append(out, d)
		}
	}
	return out
}

func (r report) loadRegressed() bool { return r.load != nil && r.load.p99Regressed() }

func (r report) resourcesRegressed() []string {
	if r.load == nil {
		return nil
	}
	var out []string
	if r.load.cpuRegressed() {
		out = append(out, "more CPU per request")
	}
	if r.load.memoryRegressed() {
		out = append(out, "more memory")
	}
	return out
}

func (r report) blocking() bool {
	return r.loadFailed || (len(r.allocIncreases()) > 0 && !r.acceptAllocs)
}

func (r report) write(out io.Writer) error {
	w := bufio.NewWriter(out)
	p := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }

	p("%s", Marker)
	p("## Performance: %s → this pull request", r.baseName)
	p("")

	allocs := r.allocIncreases()
	slower := r.changes(regressed)
	switch {
	case r.loadFailed:
		p("❌ **Blocking:** the load test against this pull request crossed its thresholds: errors, or a p99 far over budget. See the load results below and the k6 output in the run.")
	}
	switch {
	case len(allocs) > 0 && r.acceptAllocs:
		p("☑️ **Accepted:** %s more per operation, accepted with the `%s` label.", allocate(len(allocs)), AcceptLabel)
	case len(allocs) > 0:
		p("❌ **Blocking:** %s more per operation. Allocation counts are exact, so this is a real change, not noise. If it's intended, add the `%s` label.", allocate(len(allocs)), AcceptLabel)
	}
	if n := len(slower); n > 0 || r.loadRegressed() || len(r.resourcesRegressed()) > 0 {
		var what []string
		if n > 0 {
			what = append(what, count(n, "benchmark")+" slower or larger")
		}
		if r.loadRegressed() {
			what = append(what, "a higher p99 under load")
		}
		what = append(what, r.resourcesRegressed()...)
		p("⚠️ **Possible regression:** %s. Timings vary on shared runners, so this doesn't block; confirm it on a quiet machine with `make perf-compare`.", strings.Join(what, ", and "))
	}
	if !r.loadFailed && len(allocs) == 0 && len(slower) == 0 && !r.loadRegressed() && len(r.resourcesRegressed()) == 0 {
		p("✅ No regressions found.")
	}
	if n := len(r.changes(improved)); n > 0 {
		p("")
		p("🚀 %s faster or leaner.", count(n, "measurement"))
	}

	section := func(title string, ds []benchDelta) {
		if len(ds) == 0 {
			return
		}
		p("")
		p("### %s", title)
		p("")
		p("| Benchmark | Measure | %s | This PR | Change |", r.baseName)
		p("| --- | --- | ---: | ---: | ---: |")
		for _, d := range ds {
			p("| %s | %s | %s | %s | %s |", benchName(d.benchKey), unitLabel(d.unit), formatValue(d.unit, d.base), formatValue(d.unit, d.head), d.delta)
		}
	}
	section("More allocations", allocs)
	section("Possibly slower or larger", slower)
	section("Improvements", r.changes(improved))

	if r.load != nil {
		l := *r.load
		p("")
		p("### Load (k6, %d runs each, interleaved)", min(l.base.runs, l.head.runs))
		p("")
		p("| | %s | This PR | Change |", r.baseName)
		p("| --- | ---: | ---: | ---: |")
		p("| Median latency | %.2fms | %.2fms | %s |", l.base.median, l.head.median, pct(l.base.median, l.head.median))
		p("| p99 latency | %.2fms | %.2fms | %s |", l.base.p99, l.head.p99, pct(l.base.p99, l.head.p99))
		p("| Errors | %.2f%% | %.2f%% | |", l.base.errorRate*100, l.head.errorRate*100)
		p("| Requests | %d | %d | |", l.base.requests, l.head.requests)

		if l.hasStats() || l.hasMetrics() {
			p("")
			p("### Resources (the API, during the load test)")
			p("")
			p("| | %s | This PR | Change |", r.baseName)
			p("| --- | ---: | ---: | ---: |")
			if l.hasMetrics() {
				b, h := l.base.cpuPerRequest()*1e6, l.head.cpuPerRequest()*1e6
				p("| CPU per request | %.0fµs | %.0fµs | %s |", b, h, pct(b, h))
			}
			if l.hasStats() {
				p("| CPU, container average | %.1f%% | %.1f%% | %s |", l.base.cpuPercent, l.head.cpuPercent, pct(l.base.cpuPercent, l.head.cpuPercent))
				p("| Memory, container peak | %.1f MiB | %.1f MiB | %s |", l.base.memoryPeakMiB, l.head.memoryPeakMiB, pct(l.base.memoryPeakMiB, l.head.memoryPeakMiB))
			}
			if l.hasMetrics() {
				b, h := l.base.allocsPerRequest(), l.head.allocsPerRequest()
				p("| Allocated per request | %s | %s | %s |", formatValue(unitBytes, b), formatValue(unitBytes, h), pct(b, h))
				p("| GC cycles per 1,000 requests | %.2f | %.2f | %s |", l.base.gcPer1000(), l.head.gcPer1000(), pct(l.base.gcPer1000(), l.head.gcPer1000()))
				p("| Goroutines at the end | %.0f | %.0f | |", l.base.goroutinesMax, l.head.goroutinesMax)
			}
			p("")
			if l.hasMetrics() {
				p("CPU per request is the API process's own CPU time divided by the requests it served, so it doesn't depend on how busy the runner was. 100%% CPU is one core.")
			} else {
				p("100%% CPU is one core. %s's API doesn't serve its runtime metrics, so the comparisons that need them start once this lands.", r.baseName)
			}
		}

		if l.head.validatorRuns > 0 || l.base.validatorRuns > 0 {
			renderValidator(p, r.baseName, l)
		}
	}

	if len(r.bench.deltas) > 0 {
		p("")
		p("<details><summary>All benchmarks (%s)</summary>", count(len(benchNames(r.bench.deltas)), "benchmark"))
		p("")
		p("| Benchmark | Time | | Allocations | | Bytes | |")
		p("| --- | ---: | ---: | ---: | ---: | ---: | ---: |")
		for _, k := range benchNames(r.bench.deltas) {
			row := []string{benchName(k)}
			for _, unit := range []string{unitTime, unitAllocs, unitBytes} {
				d, ok := find(r.bench.deltas, benchKey{k.pkg, k.name, unit})
				if !ok {
					row = append(row, "", "")
					continue
				}
				row = append(row, formatValue(unit, d.base)+" → "+formatValue(unit, d.head), d.delta)
			}
			p("| %s |", strings.Join(row, " | "))
		}
		p("")
		p("`~` means no statistically significant difference (p ≥ 0.05). Medians of %d interleaved runs.", minRuns(r.bench.deltas))
		p("")
		p("</details>")
	}
	if len(r.bench.added) > 0 {
		p("")
		p("New benchmarks, with nothing to compare against yet: %s.", codeList(r.bench.added))
	}
	if len(r.bench.removed) > 0 {
		p("")
		p("Benchmarks only in %s: %s.", r.baseName, codeList(r.bench.removed))
	}

	p("")
	footer := "Both versions ran interleaved on the same machine. Only allocation increases and crossed load thresholds block."
	if r.docsURL != "" {
		footer += fmt.Sprintf(" [How to read this](%s).", r.docsURL)
	}
	if r.detailsURL != "" {
		footer += fmt.Sprintf(" [Full results](%s).", r.detailsURL)
	}
	p("<sub>%s</sub>", footer)
	return w.Flush()
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// allocate says how many benchmarks allocate more, with the verb agreeing.
func allocate(n int) string {
	if n == 1 {
		return "1 benchmark allocates"
	}
	return fmt.Sprintf("%d benchmarks allocate", n)
}

func pct(base, head float64) string {
	if base == 0 {
		return ""
	}
	return fmt.Sprintf("%+.1f%%", (head-base)/base*100)
}

func unitLabel(unit string) string {
	switch unit {
	case unitTime:
		return "time/op"
	default:
		return unit
	}
}

// benchName shows a benchmark as Name (package), with the package's path
// short, e.g. "API/GET_/items (cmd/api)".
func benchName(k benchKey) string {
	return fmt.Sprintf("`%s` (%s)", k.name, k.pkg)
}

// benchNames returns each benchmark once, in order, without its unit.
func benchNames(ds []benchDelta) []benchKey {
	var out []benchKey
	seen := map[benchKey]bool{}
	for _, d := range ds {
		k := benchKey{d.pkg, d.name, ""}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func find(ds []benchDelta, k benchKey) (benchDelta, bool) {
	for _, d := range ds {
		if d.benchKey == k {
			return d, true
		}
	}
	return benchDelta{}, false
}

func minRuns(ds []benchDelta) int {
	n := math.MaxInt
	for _, d := range ds {
		n = min(n, d.n)
	}
	return n
}

func codeList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	return strings.Join(quoted, ", ")
}

// renderValidator writes the calls to the request-validation service during
// the load test: their time, from the API's histogram, and how many there
// were per request. A side that made none (a base from before the check, or
// without VALIDATOR_URL) shows a dash.
func renderValidator(p func(string, ...any), baseName string, l loadComparison) {
	ms := func(s loadSide, q float64) string {
		if s.validatorRuns == 0 {
			return "—"
		}
		return fmt.Sprintf("%.2fms", s.validatorQuantile(q)*1000)
	}
	change := func(q float64) string {
		if l.base.validatorRuns == 0 || l.head.validatorRuns == 0 {
			return ""
		}
		return pct(l.base.validatorQuantile(q), l.head.validatorQuantile(q))
	}
	perRequest := func(s loadSide) string {
		if s.validatorRuns == 0 {
			return "—"
		}
		return fmt.Sprintf("%.2f", s.validatorPerRequest())
	}
	p("")
	p("### Request validation (the API's calls to the validation service, during the load test)")
	p("")
	p("| | %s | This PR | Change |", baseName)
	p("| --- | ---: | ---: | ---: |")
	row := func(label, base, head, change string) {
		if change == "" {
			p("| %s | %s | %s | |", label, base, head)
			return
		}
		p("| %s | %s | %s | %s |", label, base, head, change)
	}
	row("Median call", ms(l.base, 0.5), ms(l.head, 0.5), change(0.5))
	row("p99 call", ms(l.base, 0.99), ms(l.head, 0.99), change(0.99))
	row("Calls per request", perRequest(l.base), perRequest(l.head), "")
	p("")
	p("From the API's own histogram (validator_call_duration_seconds), so it's the check's share of each request's latency: estimated within the histogram's buckets, like Prometheus's histogram_quantile.")
}
