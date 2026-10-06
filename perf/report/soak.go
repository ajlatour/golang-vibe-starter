package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// `report soak` judges a soak run (perf/soak.sh): whether the API's heap or
// goroutines grew while the load held steady, which is what a leak looks
// like. It reads the samples perf/soak.sh took of the API's runtime metrics,
// once a minute, writes a Markdown summary, and exits 1 if either grew. The
// nightly Soak workflow runs it; see docs/performance.md#soak.
//
// A leak shows in the low points. The heap rises and falls with every GC
// cycle, and goroutines with the requests in flight, so what's compared is
// the lowest value in the first third of the run with the lowest in the last
// third, after the warm-up: a leak lifts the floor, while noise moves the
// peaks.

// soakSample is one scrape of the API's metrics.
type soakSample struct {
	Seconds    float64 // since sampling started
	HeapLive   float64 // go_gc_heap_live_bytes: the heap the last GC kept
	Goroutines float64 // go_sched_goroutines_goroutines
	Mapped     float64 // go_memory_classes_total_bytes: all the runtime holds
}

// The growth a soak run tolerates between the first third and the last, in
// the low points: both a share and an absolute amount must be exceeded, so a
// small heap's noise and a large one's don't fail it.
const (
	soakHeapGrowth       = 0.25
	soakHeapGrowthBytes  = 4 << 20
	soakGoroutineGrowth  = 0.25
	soakGoroutineGrowthN = 10
	// soakMinSamples is the fewest samples after the warm-up that can be
	// split into thirds and still say something.
	soakMinSamples = 6
)

// A soakRecord is one soak run's result, kept on the perf-data branch under
// soak/ like the other records.
type soakRecord struct {
	SHA        string    `json:"sha"`
	Time       string    `json:"time"`
	Subject    string    `json:"subject"`
	Minutes    float64   `json:"minutes"` // judged, after the warm-up
	HeapLive   soakRange `json:"heap_live_bytes"`
	Goroutines soakRange `json:"goroutines"`
	Mapped     soakRange `json:"mapped_bytes"`
	// Grew names what grew past its tolerance: "heap", "goroutines". Empty
	// when the run passed.
	Grew []string `json:"grew"`
}

// soakRange is a metric's low point in the first third and the last.
type soakRange struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func runSoak(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("soak", flag.ContinueOnError)
	samplesFile := fs.String("samples", "", "perf/soak.sh's samples, a TSV file")
	skip := fs.Duration("skip", 5*time.Minute, "the warm-up to leave out: the heap and the connection pool settle")
	recordFile := fs.String("record", "", "also write the result as JSON to this file, for the perf-data branch")
	sha := fs.String("sha", "", "the commit, for the record")
	subject := fs.String("subject", "", "the commit's subject line, for the record")
	when := fs.String("time", time.Now().UTC().Format(time.RFC3339), "when it was measured, for the record")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *samplesFile == "" {
		return errors.New("soak needs -samples")
	}
	f, err := os.Open(*samplesFile)
	if err != nil {
		return err
	}
	defer f.Close()
	samples, err := readSoakSamples(f)
	if err != nil {
		return fmt.Errorf("%s: %w", *samplesFile, err)
	}
	r, err := judgeSoak(samples, *skip)
	if err != nil {
		return err
	}
	r.SHA, r.Subject, r.Time = *sha, *subject, *when
	writeSoakSummary(out, r)
	if *recordFile != "" {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*recordFile, append(b, '\n'), 0o644); err != nil { // #nosec G306 -- a record for a public branch
			return err
		}
	}
	if len(r.Grew) > 0 {
		return errSoakGrew
	}
	return nil
}

// errSoakGrew is the verdict, not a failure to judge: main exits 1 for it.
var errSoakGrew = errors.New("the API grew during the soak run")

// readSoakSamples reads the TSV perf/soak.sh writes: a header naming the
// columns, then one line per scrape. Columns are found by name, so the
// script can add more.
func readSoakSamples(r io.Reader) ([]soakSample, error) {
	sc := bufio.NewScanner(r)
	if !sc.Scan() {
		return nil, errors.New("empty: no header")
	}
	cols := map[string]int{}
	for i, name := range strings.Split(sc.Text(), "\t") {
		cols[name] = i
	}
	for _, name := range []string{"seconds", "heap_live_bytes", "goroutines", "mapped_bytes"} {
		if _, ok := cols[name]; !ok {
			return nil, fmt.Errorf("no %s column", name)
		}
	}
	var samples []soakSample
	for line := 2; sc.Scan(); line++ {
		fields := strings.Split(sc.Text(), "\t")
		num := func(name string) (float64, error) {
			i := cols[name]
			if i >= len(fields) {
				return 0, fmt.Errorf("line %d: no %s", line, name)
			}
			v, err := strconv.ParseFloat(fields[i], 64)
			if err != nil {
				return 0, fmt.Errorf("line %d: %s: %w", line, name, err)
			}
			return v, nil
		}
		var s soakSample
		var err error
		for _, c := range []struct {
			name string
			dst  *float64
		}{{"seconds", &s.Seconds}, {"heap_live_bytes", &s.HeapLive}, {"goroutines", &s.Goroutines}, {"mapped_bytes", &s.Mapped}} {
			if *c.dst, err = num(c.name); err != nil {
				return nil, err
			}
		}
		samples = append(samples, s)
	}
	return samples, sc.Err()
}

// judgeSoak compares the low points of the first and last thirds of the
// samples after skip.
func judgeSoak(samples []soakSample, skip time.Duration) (soakRecord, error) {
	i := slices.IndexFunc(samples, func(s soakSample) bool { return s.Seconds >= skip.Seconds() })
	if i < 0 || len(samples)-i < soakMinSamples {
		return soakRecord{}, fmt.Errorf("%d samples after the first %v; judging growth needs %d: was the API's /metrics reachable throughout?",
			max(0, len(samples)-max(i, 0)), skip, soakMinSamples)
	}
	samples = samples[i:]
	third := len(samples) / 3
	first, last := samples[:third], samples[len(samples)-third:]
	low := func(ss []soakSample, v func(soakSample) float64) float64 {
		m := math.Inf(1)
		for _, s := range ss {
			m = min(m, v(s))
		}
		return m
	}
	rng := func(v func(soakSample) float64) soakRange {
		return soakRange{Start: low(first, v), End: low(last, v)}
	}
	r := soakRecord{
		Minutes:    (samples[len(samples)-1].Seconds - samples[0].Seconds) / 60,
		HeapLive:   rng(func(s soakSample) float64 { return s.HeapLive }),
		Goroutines: rng(func(s soakSample) float64 { return s.Goroutines }),
		Mapped:     rng(func(s soakSample) float64 { return s.Mapped }),
		Grew:       []string{},
	}
	if grew(r.HeapLive, soakHeapGrowth, soakHeapGrowthBytes) {
		r.Grew = append(r.Grew, "heap")
	}
	if grew(r.Goroutines, soakGoroutineGrowth, soakGoroutineGrowthN) {
		r.Grew = append(r.Grew, "goroutines")
	}
	return r, nil
}

// grew reports whether r's end exceeds its start by more than both share and
// amount.
func grew(r soakRange, share, amount float64) bool {
	d := r.End - r.Start
	return d > amount && d > r.Start*share
}

func writeSoakSummary(w io.Writer, r soakRecord) {
	verdict := "✅ Nothing grew."
	if len(r.Grew) > 0 {
		verdict = "❌ Grew: " + strings.Join(r.Grew, ", ") + ". Compare heap and goroutine profiles from the start and the end of a run (`make perf-profile PROFILE=soak`) to see what holds on."
	}
	fmt.Fprintf(w, "## Soak test\n\n%s\n\n", verdict)
	fmt.Fprintf(w, "The lowest value in the first third of the run and in the last third, over %.0f minutes of steady load after the warm-up.\n\n", r.Minutes)
	fmt.Fprintln(w, "| | First third | Last third | Change | Fails over |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: |")
	mib := func(v float64) string { return fmt.Sprintf("%.1f MiB", v/(1<<20)) }
	n := func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) }
	row := func(name string, r soakRange, f func(float64) string, limit string) {
		change := "–"
		if r.Start > 0 {
			change = fmt.Sprintf("%+.0f%%", (r.End-r.Start)/r.Start*100)
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", name, f(r.Start), f(r.End), change, limit)
	}
	row("Live heap", r.HeapLive, mib, fmt.Sprintf("+%.0f%% and +%s", soakHeapGrowth*100, mib(soakHeapGrowthBytes)))
	row("Goroutines", r.Goroutines, n, fmt.Sprintf("+%.0f%% and +%d", soakGoroutineGrowth*100, soakGoroutineGrowthN))
	row("Runtime memory", r.Mapped, mib, "never: for context")
}
