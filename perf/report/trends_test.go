package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// headDir writes k6Dir's head runs, with resources, as the trends workflow
// leaves them (no base).
func headDir(t *testing.T) string {
	t.Helper()
	dir := k6Dir(t, [3]float64{1, 2, 0}, [3]float64{0.8, 2.5, 0})
	withResources(t, dir, "head", 12, 15, 0.1, 2048*1000)
	for _, f := range []string{"base-1.json", "base-2.json"} {
		os.Remove(filepath.Join(dir, f))
	}
	return dir
}

func record1(t *testing.T, sha, subject, when string) record {
	t.Helper()
	var out strings.Builder
	err := runRecord([]string{"-sha", sha, "-subject", subject, "-time", when,
		"-bench", benchOutput(t, "cmd/api", 6, map[string][3]float64{"API/GET": {40000, 17000, 138}}),
		"-load", headDir(t)}, &out)
	if err != nil {
		t.Fatal(err)
	}
	var r record
	if err := json.Unmarshal([]byte(out.String()), &r); err != nil {
		t.Fatalf("record isn't JSON: %v\n%s", err, out.String())
	}
	return r
}

func TestRecord(t *testing.T) {
	r := record1(t, "abc1234def", "Add things", "2026-10-01T10:00:00Z")
	if r.SHA != "abc1234def" || r.Subject != "Add things" || r.Time != "2026-10-01T10:00:00Z" {
		t.Errorf("identity = %+v", r)
	}
	b := r.Benchmarks["cmd/api API/GET"]
	if b["allocs/op"] != 138 || b["B/op"] != 17000 || b["sec/op"] < 40e-6 || b["sec/op"] > 41e-6 {
		t.Errorf("benchmark medians = %v", b)
	}
	l := r.Load
	if l == nil || l.MedianMS != 0.8 || l.P99MS != 2.5 || l.Requests != 2000 {
		t.Fatalf("load = %+v", l)
	}
	if l.CPUPerRequestUS == nil || math.Abs(*l.CPUPerRequestUS-100) > 1e-6 || l.MemoryPeakMiB == nil || *l.MemoryPeakMiB != 15 {
		t.Errorf("CPU per request %v µs, memory peak %v MiB; want 100 and 15", deref(l.CPUPerRequestUS), deref(l.MemoryPeakMiB))
	}
}

func TestRecordNeedsItsInputs(t *testing.T) {
	for _, args := range [][]string{{"-bench", "x.txt"}, {"-sha", "abc"}, {"-sha", "abc", "-bench", "nope.txt"}} {
		if err := runRecord(args, &strings.Builder{}); err == nil {
			t.Errorf("record %q = nil error, want one", args)
		}
	}
}

func writeRecords(t *testing.T, recs ...record) string {
	t.Helper()
	dir := t.TempDir()
	for i, r := range recs {
		b, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(dir, "2026100"+string(rune('1'+i))+"-"+r.SHA[:3]+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTrends(t *testing.T) {
	a := record1(t, "aaa1111", "First", "2026-10-01T10:00:00Z")
	b := record1(t, "bbb2222", `<script>alert("x")</script>`, "2026-10-02T10:00:00Z")
	b.Benchmarks["cmd/api API/GET"]["allocs/op"] = 140
	var out strings.Builder
	if err := runTrends([]string{"-records", writeRecords(t, a, b)}, &out); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	mustContain(t, page, "<title>Performance trends</title>", "2 commits measured, from aaa1111 (2026-10-01) to bbb2222 (2026-10-02)",
		"Latency under load", "CPU per request", "Allocations per request, by route", "Time per request, by route",
		"bbb2222 GET: 140 allocs/op")
	if n := strings.Count(page, "<section>"); n != 6 {
		t.Errorf("%d charts, want 6", n)
	}
	if strings.Contains(page, "<script>") {
		t.Error("a commit subject went into the page unescaped")
	}
}

func TestTrendsSkipsChartsWithoutData(t *testing.T) {
	a := record1(t, "aaa1111", "First", "2026-10-01T10:00:00Z")
	a.Load = nil // the load test didn't run
	var out strings.Builder
	if err := runTrends([]string{"-records", writeRecords(t, a)}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Latency under load") || !strings.Contains(out.String(), "Allocations per request") {
		t.Error("want the benchmark charts only, for a record without load results")
	}
	if err := runTrends([]string{"-records", t.TempDir()}, &strings.Builder{}); err == nil {
		t.Error("trends with no records = nil error, want one")
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
