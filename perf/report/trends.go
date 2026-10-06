package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/perf/benchmath"
)

// A record is one commit's performance, as the perf-trends workflow measures
// main after every merge: the benchmarks' medians, and the load test's
// results. A directory of them, one file per commit, is the history that
// `report trends` charts.
type record struct {
	SHA     string `json:"sha"`
	Time    string `json:"time"` // RFC 3339
	Subject string `json:"subject"`
	// Benchmarks maps "pkg Name" to unit (sec/op, B/op, allocs/op) to the
	// median of the runs.
	Benchmarks map[string]map[string]float64 `json:"benchmarks"`
	Load       *loadRecord                   `json:"load,omitempty"`
}

type loadRecord struct {
	MedianMS  float64 `json:"median_ms"`
	P99MS     float64 `json:"p99_ms"`
	ErrorRate float64 `json:"error_rate"`
	Requests  int     `json:"requests"`
	// From the API's own metrics and docker stats, when recorded.
	CPUPerRequestUS      *float64 `json:"cpu_per_request_us,omitempty"`
	AllocPerRequestBytes *float64 `json:"alloc_per_request_bytes,omitempty"`
	GCPer1000            *float64 `json:"gc_per_1000_requests,omitempty"`
	MemoryPeakMiB        *float64 `json:"memory_peak_mib,omitempty"`
}

// runRecord is `report record`: one commit's results as a record.
func runRecord(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	benchFile := fs.String("bench", "", "the commit's benchmark output")
	loadDir := fs.String("load", "", "a directory of k6 summaries, head-<n>.json")
	sha := fs.String("sha", "", "the commit")
	subject := fs.String("subject", "", "the commit's subject line")
	when := fs.String("time", time.Now().UTC().Format(time.RFC3339), "when it was measured")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sha == "" || *benchFile == "" {
		return fmt.Errorf("record needs -sha and -bench")
	}
	r := record{SHA: *sha, Time: *when, Subject: *subject, Benchmarks: map[string]map[string]float64{}}

	runs, err := readBenchFile(*benchFile)
	if err != nil {
		return err
	}
	if len(runs.order) == 0 {
		return fmt.Errorf("%s: no benchmark results", *benchFile)
	}
	thresholds := benchmath.DefaultThresholds
	for _, k := range runs.order {
		name := k.pkg + " " + k.name
		if r.Benchmarks[name] == nil {
			r.Benchmarks[name] = map[string]float64{}
		}
		s := benchmath.NewSample(runs.values[k], &thresholds)
		r.Benchmarks[name][k.unit] = benchmath.AssumeNothing.Summary(s, 0.95).Center
	}

	if *loadDir != "" {
		side, err := readLoadSide(*loadDir, "head")
		if err != nil {
			return err
		}
		l := &loadRecord{MedianMS: side.median, P99MS: side.p99, ErrorRate: side.errorRate, Requests: side.requests}
		if side.metricRuns > 0 {
			l.CPUPerRequestUS = ptr(side.cpuPerRequest() * 1e6)
			l.AllocPerRequestBytes = ptr(side.allocsPerRequest())
			l.GCPer1000 = ptr(side.gcPer1000())
		}
		if side.statsRuns > 0 {
			l.MemoryPeakMiB = ptr(side.memoryPeakMiB)
		}
		r.Load = l
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func ptr(v float64) *float64 { return &v }

// readRecords reads every record in dir, oldest first by file name, which the
// workflow starts with the time.
func readRecords(dir string) ([]record, error) {
	var out []record
	err := readDir(dir, func(path string, b []byte) error {
		var r record
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

// readDir calls f with each JSON file in dir, in name order, and fails if
// there are none.
func readDir(dir string, f func(path string, b []byte) error) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s: no records", dir)
	}
	slices.Sort(files)
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := f(path, b); err != nil {
			return err
		}
	}
	return nil
}

// series is one line on a chart: a value per record, NaN where it has none.
type series struct {
	name   string
	values []float64
}

type chart struct {
	title, unit, note string
	series            []series
	// points are the commits along the x axis, one per value of each
	// series; nil for the performance records'.
	points []record
}

// runTrends is `report trends`: an HTML page charting the records, or with
// -fragment, the charts alone, for the docs site's Performance history page
// (docs/site/build.sh), which styles them (docs/site/site.css).
func runTrends(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("trends", flag.ContinueOnError)
	dir := fs.String("records", "", "the directory of records")
	ciDir := fs.String("ci-records", "", "a directory of CI records (report ci-record), to chart too")
	fragment := fs.Bool("fragment", false, "write the charts alone, as an HTML fragment for a docs page")
	if err := fs.Parse(args); err != nil {
		return err
	}
	recs, err := readRecords(*dir)
	if err != nil {
		return err
	}
	var ciRecs []ciRecord
	if *ciDir != "" {
		if ciRecs, err = readCIRecords(*ciDir); err != nil {
			return err
		}
	}

	load := func(name string, f func(*loadRecord) *float64) series {
		s := series{name: name}
		for _, r := range recs {
			v := math.NaN()
			if r.Load != nil {
				if p := f(r.Load); p != nil {
					v = *p
				}
			}
			s.values = append(s.values, v)
		}
		return s
	}
	bench := func(unit string, scale float64) []series {
		var names []string
		for _, r := range recs {
			for n := range r.Benchmarks {
				if strings.HasPrefix(n, "cmd/api ") && !slices.Contains(names, n) {
					names = append(names, n)
				}
			}
		}
		slices.Sort(names)
		var out []series
		for _, n := range names {
			s := series{name: strings.TrimPrefix(strings.TrimPrefix(n, "cmd/api "), "API/")}
			for _, r := range recs {
				v, ok := r.Benchmarks[n][unit]
				if !ok {
					v = math.NaN()
				}
				s.values = append(s.values, v*scale)
			}
			out = append(out, s)
		}
		return out
	}

	charts := []chart{
		{title: "Latency under load", unit: "ms", note: "k6's compare profile, steady phase. Shared runners: treat small moves as noise.",
			series: []series{load("p99", func(l *loadRecord) *float64 { return &l.P99MS }), load("median", func(l *loadRecord) *float64 { return &l.MedianMS })}},
		{title: "CPU per request", unit: "µs", note: "The API process's own CPU time per request served, so independent of how busy the runner was.",
			series: []series{load("CPU", func(l *loadRecord) *float64 { return l.CPUPerRequestUS })}},
		{title: "Allocated per request", unit: "KiB", note: "Bytes the API allocated per request under load.",
			series: []series{load("allocated", func(l *loadRecord) *float64 {
				if l.AllocPerRequestBytes == nil {
					return nil
				}
				return ptr(*l.AllocPerRequestBytes / 1024)
			})}},
		{title: "Memory, container peak", unit: "MiB", note: "The API container's peak during the load test.",
			series: []series{load("memory", func(l *loadRecord) *float64 { return l.MemoryPeakMiB })}},
		{title: "Allocations per request, by route", unit: "allocs/op", note: "BenchmarkAPI: exact, so any step is a real change.",
			series: bench("allocs/op", 1)},
		{title: "Time per request, by route", unit: "µs", note: "BenchmarkAPI, in-process: the median of 6 runs on a shared runner.",
			series: bench("sec/op", 1e6)},
	}
	if len(ciRecs) > 0 {
		charts = append(charts, ciCharts(ciRecs)...)
	}
	if *fragment {
		return writeTrendsBody(out, recs, charts, true)
	}
	return writeTrendsPage(out, recs, charts)
}

var palette = []string{"#2563eb", "#dc2626", "#16a34a", "#d97706", "#9333ea", "#0891b2", "#db2777", "#65a30d"}

func writeTrendsPage(w io.Writer, recs []record, charts []chart) error {
	p := func(format string, args ...any) { fmt.Fprintf(w, format, args...) }
	p(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Performance trends</title>
<style>
:root { --bg: #ffffff; --fg: #1f2328; --muted: #656d76; --grid: #d8dee4; --card: #f6f8fa; }
@media (prefers-color-scheme: dark) { :root { --bg: #0d1117; --fg: #e6edf3; --muted: #8d96a0; --grid: #30363d; --card: #161b22; } }
body { background: var(--bg); color: var(--fg); font: 15px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 980px; padding: 24px 16px; }
h1 { font-size: 24px; margin: 0 0 4px; } h2 { font-size: 17px; margin: 0; }
p.meta, p.note { color: var(--muted); margin: 4px 0 12px; font-size: 13px; }
section { background: var(--card); border-radius: 8px; padding: 16px; margin: 16px 0; }
svg { width: 100%%; height: auto; display: block; }
svg text { fill: var(--muted); font-size: 11px; } svg .grid { stroke: var(--grid); stroke-width: 1; }
.legend { display: flex; flex-wrap: wrap; gap: 4px 16px; font-size: 13px; margin-top: 8px; }
.legend span::before { content: ""; display: inline-block; width: 10px; height: 10px; border-radius: 2px; margin-right: 6px; background: var(--c); }
</style></head><body>
<h1>Performance trends</h1>
`)
	if err := writeTrendsBody(w, recs, charts, false); err != nil {
		return err
	}
	p("</body></html>\n")
	return nil
}

// writeTrendsBody writes what the records cover, then the charts. As a
// fragment, it's wrapped in <div class="perf-history"> for the docs site's
// styles, and has no blank line: Markdown ends a raw HTML block at one.
func writeTrendsBody(w io.Writer, recs []record, charts []chart, fragment bool) error {
	p := func(format string, args ...any) { fmt.Fprintf(w, format, args...) }
	first, last := recs[0], recs[len(recs)-1]
	if fragment {
		p("<div class=\"perf-history\">\n")
	}
	p("<p class=\"meta\">main, %d commits measured, from %s (%s) to %s (%s). Hover a point for its commit.</p>\n",
		len(recs), html.EscapeString(short(first.SHA)), html.EscapeString(day(first.Time)), html.EscapeString(short(last.SHA)), html.EscapeString(day(last.Time)))
	for _, c := range charts {
		if !hasData(c) {
			continue
		}
		p("<section><h2>%s <span style=\"color:var(--muted);font-weight:normal\">(%s)</span></h2><p class=\"note\">%s</p>\n", html.EscapeString(c.title), html.EscapeString(c.unit), html.EscapeString(c.note))
		points := c.points
		if points == nil {
			points = recs
		}
		writeChart(w, points, c)
		p(`<div class="legend">`)
		for i, s := range c.series {
			p(`<span style="--c:%s">%s</span>`, palette[i%len(palette)], html.EscapeString(s.name))
		}
		p("</div></section>\n")
	}
	if fragment {
		p("</div>\n")
	}
	return nil
}

func hasData(c chart) bool {
	for _, s := range c.series {
		for _, v := range s.values {
			if !math.IsNaN(v) {
				return true
			}
		}
	}
	return false
}

// writeChart draws c as an SVG line chart: one x position per record, y from
// zero to a little over the largest value.
func writeChart(w io.Writer, recs []record, c chart) {
	const width, height, left, right, top, bottom = 900.0, 240.0, 56.0, 34.0, 10.0, 26.0
	maxV := 0.0
	for _, s := range c.series {
		for _, v := range s.values {
			if !math.IsNaN(v) {
				maxV = math.Max(maxV, v)
			}
		}
	}
	if maxV == 0 {
		maxV = 1
	}
	maxV *= 1.1
	n := len(recs)
	x := func(i int) float64 {
		if n == 1 {
			return left + (width-left-right)/2
		}
		return left + float64(i)*(width-left-right)/float64(n-1)
	}
	y := func(v float64) float64 { return top + (height-top-bottom)*(1-v/maxV) }

	fmt.Fprintf(w, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, width, height, html.EscapeString(c.title))
	for i := 0; i <= 4; i++ {
		v := maxV * float64(i) / 4
		fmt.Fprintf(w, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/><text x="%.1f" y="%.1f" text-anchor="end">%s</text>`,
			left, width-right, y(v), y(v), left-6, y(v)+4, html.EscapeString(formatTick(v)))
	}
	step := max(1, n/8)
	for i := 0; i < n; i += step {
		fmt.Fprintf(w, `<text x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x(i), height-6, html.EscapeString(short(recs[i].SHA)))
	}
	for si, s := range c.series {
		color := palette[si%len(palette)]
		var pts []string
		for i, v := range s.values {
			if math.IsNaN(v) {
				continue
			}
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(i), y(v)))
		}
		if len(pts) > 1 {
			fmt.Fprintf(w, `<polyline fill="none" stroke="%s" stroke-width="2" points="%s"/>`, color, strings.Join(pts, " "))
		}
		for i, v := range s.values {
			if math.IsNaN(v) {
				continue
			}
			// The hover text's second line, the subject, follows "&#10;", not a
			// line break: every line of the output then starts with a tag, so
			// Markdown keeps it all as raw HTML (see writeTrendsBody).
			fmt.Fprintf(w, `<circle cx="%.1f" cy="%.1f" r="3.5" fill="%s"><title>%s %s: %s %s&#10;%s</title></circle>`, x(i), y(v), color, html.EscapeString(short(recs[i].SHA)), html.EscapeString(s.name),
				html.EscapeString(formatTick(v)), html.EscapeString(c.unit), html.EscapeString(recs[i].Subject))
		}
	}
	fmt.Fprint(w, "</svg>\n")
}

func formatTick(v float64) string {
	switch {
	case v == 0:
		return "0"
	case v >= 100:
		return fmt.Sprintf("%.0f", v)
	case v >= 10:
		return fmt.Sprintf("%.1f", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func day(t string) string {
	if len(t) >= 10 {
		return t[:10]
	}
	return t
}
