// Command report compares the performance of a pull request with its base
// branch and writes the result as Markdown, for the comment the perf workflow
// keeps on each pull request. `make perf-compare` runs it locally too.
//
//	go run . -bench-base base.txt -bench-head head.txt -load ab/ > report.md
//
// The inputs come from perf/bench-ab.sh (Go benchmark output, both versions
// interleaved on one machine) and perf/load-ab.sh (k6 summaries, likewise).
// Benchmarks are compared with benchstat's own statistics (golang.org/x/perf),
// so the report agrees with `make bench-compare`.
//
// It exits 1 when the pull request has a blocking regression: an allocation
// increase on a benchmarked path (allocations per operation don't vary
// between runs or machines, so any increase is real), or a k6 run that
// crossed its profile's thresholds. -accept-allocs (the perf-ok label) accepts
// an intended allocation increase. Timing changes are reported, never
// blocking, because shared CI runners are too noisy to gate on.
//
// This is its own module, like integration/, so that the service itself
// stays standard library only.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	var opts options
	flag.StringVar(&opts.benchBase, "bench-base", "", "the base's benchmark output")
	flag.StringVar(&opts.benchHead, "bench-head", "", "the pull request's benchmark output")
	flag.StringVar(&opts.loadDir, "load", "", "a directory of k6 summaries, base-<n>.json and head-<n>.json")
	flag.BoolVar(&opts.loadFailed, "load-failed", false, "a k6 run against the pull request crossed its thresholds")
	flag.BoolVar(&opts.acceptAllocs, "accept-allocs", false, "accept allocation increases (the perf-ok label)")
	flag.StringVar(&opts.baseName, "base-name", "the base", "what to call the base in the report, e.g. main")
	flag.StringVar(&opts.detailsURL, "details-url", "", "a link to the full results, e.g. the CI run")
	flag.StringVar(&opts.docsURL, "docs-url", "", "a link to docs/performance.md#pull-request-reports")
	flag.Parse()

	blocking, err := run(os.Stdout, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "report:", err)
		os.Exit(2)
	}
	if blocking {
		os.Exit(1)
	}
}

type options struct {
	benchBase, benchHead string
	loadDir              string
	loadFailed           bool
	acceptAllocs         bool
	baseName             string
	detailsURL           string
	docsURL              string
}

// run reads the inputs, writes the report to w, and reports whether it has a
// blocking regression that hasn't been accepted.
func run(w io.Writer, opts options) (blocking bool, err error) {
	var r report
	r.baseName = opts.baseName
	r.detailsURL = opts.detailsURL
	r.docsURL = opts.docsURL
	r.acceptAllocs = opts.acceptAllocs
	r.loadFailed = opts.loadFailed
	if opts.benchBase != "" || opts.benchHead != "" {
		base, err := readBenchFile(opts.benchBase)
		if err != nil {
			return false, err
		}
		head, err := readBenchFile(opts.benchHead)
		if err != nil {
			return false, err
		}
		r.bench = compareBench(base, head)
	}
	if opts.loadDir != "" {
		load, err := readLoad(opts.loadDir)
		if err != nil {
			return false, err
		}
		r.load = &load
	}
	if err := r.write(w); err != nil {
		return false, err
	}
	return r.blocking(), nil
}
