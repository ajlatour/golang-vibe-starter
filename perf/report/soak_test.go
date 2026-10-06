package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// soakTSV writes samples a minute apart, as perf/soak.sh does, with the heap
// and goroutines given per minute by heap and goroutines.
func soakTSV(t *testing.T, minutes int, heap, goroutines func(m int) float64) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("seconds\theap_live_bytes\tgoroutines\tmapped_bytes\n")
	for m := 0; m <= minutes; m++ {
		fmt.Fprintf(&b, "%d\t%.0f\t%.0f\t%d\n", m*60, heap(m), goroutines(m), 40<<20)
	}
	p := filepath.Join(t.TempDir(), "soak.samples.tsv")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// sawtooth is a heap that a GC cycle brings back to base every few minutes.
func sawtooth(base float64) func(int) float64 {
	return func(m int) float64 { return base + float64(m%4)*3*mib }
}

const mib = 1 << 20

func flat(v float64) func(int) float64 { return func(int) float64 { return v } }

func TestSoak(t *testing.T) {
	for _, tc := range []struct {
		name       string
		heap       func(int) float64
		goroutines func(int) float64
		grew       []string
	}{
		{"steady", sawtooth(10 << 20), flat(30), nil},
		// Peaks that wander don't count; only the floor does.
		{"noisy peaks", func(m int) float64 {
			if m%5 == 4 {
				return 40 << 20
			}
			return 10 << 20
		}, flat(30), nil},
		// Peaks that climb over a floor that holds: garbage that a GC
		// cycle collects, not a leak.
		{"rising peaks", func(m int) float64 {
			if m%3 == 2 {
				return float64(10+m) * mib
			}
			return 10 * mib
		}, flat(30), nil},
		// A warm-up that grows, then holds, isn't a leak.
		{"warm-up growth", func(m int) float64 { return float64(min(m, 5)) * 4 * mib }, flat(30), nil},
		{"heap leak", func(m int) float64 { return 10<<20 + float64(m)*mib }, flat(30), []string{"heap"}},
		// A large heap's small drift is under the share.
		{"large heap drift", func(m int) float64 { return 200<<20 + float64(m)*mib }, flat(30), nil},
		// A small heap's growth is under the bytes.
		{"small heap growth", func(m int) float64 { return 2<<20 + float64(m)*mib/16 }, flat(30), nil},
		{"goroutine leak", sawtooth(10 << 20), func(m int) float64 { return 30 + float64(m) }, []string{"goroutines"}},
		{"both", func(m int) float64 { return 10<<20 + float64(m)*mib }, func(m int) float64 { return 30 + float64(m) }, []string{"heap", "goroutines"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			samples := soakTSV(t, 30, tc.heap, tc.goroutines)
			record := filepath.Join(t.TempDir(), "soak.json")
			var out strings.Builder
			err := runSoak([]string{"-samples", samples, "-record", record, "-sha", "abc1234", "-time", "2026-10-06T03:00:00Z"}, &out)
			if (err != nil) != (tc.grew != nil) || (err != nil && err != errSoakGrew) {
				t.Fatalf("err = %v, want growth: %v\n%s", err, tc.grew, out.String())
			}
			b, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			var r soakRecord
			if err := json.Unmarshal(b, &r); err != nil {
				t.Fatal(err)
			}
			if want := tc.grew; !slices.Equal(r.Grew, want) && (len(r.Grew) > 0 || len(want) > 0) {
				t.Errorf("grew = %v, want %v", r.Grew, want)
			}
			if r.SHA != "abc1234" || r.Time != "2026-10-06T03:00:00Z" || r.Minutes != 25 {
				t.Errorf("record = %+v", r)
			}
			if tc.grew == nil && !strings.Contains(out.String(), "Nothing grew") {
				t.Errorf("summary doesn't say it passed:\n%s", out.String())
			}
			if tc.grew != nil && !strings.Contains(out.String(), "Grew: "+strings.Join(tc.grew, ", ")) {
				t.Errorf("summary doesn't name what grew:\n%s", out.String())
			}
		})
	}
}

// A run whose metrics were unreachable for most of it can't be judged
// either way, which is an error, not a pass.
func TestSoakTooFewSamples(t *testing.T) {
	samples := soakTSV(t, 9, flat(10<<20), flat(30))
	err := runSoak([]string{"-samples", samples}, &strings.Builder{})
	if err == nil || err == errSoakGrew || !strings.Contains(err.Error(), "5 samples after the first 5m0s") {
		t.Fatalf("err = %v, want too few samples", err)
	}
}

func TestReadSoakSamples(t *testing.T) {
	// Columns are found by name, in any order, with extras ignored.
	got, err := readSoakSamples(strings.NewReader("goroutines\textra\tseconds\tmapped_bytes\theap_live_bytes\n12\tx\t60\t3\t4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []soakSample{{Seconds: 60, HeapLive: 4, Goroutines: 12, Mapped: 3}}; !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	for name, in := range map[string]string{
		"empty":          "",
		"missing column": "seconds\theap_live_bytes\tgoroutines\n",
		"short line":     "seconds\theap_live_bytes\tgoroutines\tmapped_bytes\n60\t1\t2\n",
		"not a number":   "seconds\theap_live_bytes\tgoroutines\tmapped_bytes\n60\t1\tmany\t3\n",
	} {
		if _, err := readSoakSamples(strings.NewReader(in)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestGrew(t *testing.T) {
	for _, tc := range []struct {
		r    soakRange
		want bool
	}{
		{soakRange{100, 100}, false},
		{soakRange{100, 90}, false},
		{soakRange{100, 126}, false}, // over the share, not the amount
		{soakRange{100, 200}, true},
		{soakRange{1000, 1050}, false}, // over the amount, not the share
	} {
		if got := grew(tc.r, 0.25, 30); got != tc.want {
			t.Errorf("grew(%+v) = %v, want %v", tc.r, got, tc.want)
		}
	}
}
