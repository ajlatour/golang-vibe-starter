package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ciRun is a workflow run as the GitHub API gives it, cut to what a record
// reads.
const ciRun = `{
  "id": 42, "head_sha": "abc1234def", "run_attempt": 1, "conclusion": "success",
  "run_started_at": "2026-10-05T10:00:00Z",
  "head_commit": {"message": "Merge pull request #9\n\nAdd things"}
}`

// ciJobs is the run's jobs, one object each, as gh api --paginate --jq
// '.jobs[]' prints them: test waits 3s for a runner and runs 60s; lint starts
// later and finishes last; a skipped job, and a skipped step, are left out.
const ciJobs = `{"name": "test", "conclusion": "success", "created_at": "2026-10-05T10:00:01Z", "started_at": "2026-10-05T10:00:04Z", "completed_at": "2026-10-05T10:01:04Z",
 "steps": [{"name": "Set up job", "conclusion": "success", "started_at": "2026-10-05T10:00:04Z", "completed_at": "2026-10-05T10:00:06Z"},
           {"name": "Unit tests", "conclusion": "success", "started_at": "2026-10-05T10:00:06Z", "completed_at": "2026-10-05T10:01:00Z"},
           {"name": "Upload", "conclusion": "skipped", "started_at": "2026-10-05T10:01:00Z", "completed_at": "2026-10-05T10:01:00Z"},
           {"name": "Post", "conclusion": "success", "started_at": "2026-10-05T10:01:01Z", "completed_at": "2026-10-05T10:01:00Z"}]}
{"name": "lint", "conclusion": "success", "created_at": "2026-10-05T10:00:01Z", "started_at": "2026-10-05T10:00:10Z", "completed_at": "2026-10-05T10:02:10Z", "steps": []}
{"name": "deploy", "conclusion": "skipped", "created_at": "2026-10-05T10:00:01Z", "started_at": "2026-10-05T10:00:01Z", "completed_at": "2026-10-05T10:00:01Z", "steps": []}
`

// lastLine is the last job in jobs: the skipped one.
func lastLine(jobs string) string {
	lines := strings.Split(strings.TrimSpace(jobs), "\n")
	return lines[len(lines)-1]
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func ciRecord1(t *testing.T, run, jobs string) (ciRecord, error) {
	t.Helper()
	var out strings.Builder
	err := runCIRecord([]string{"-run", writeFile(t, "run.json", run), "-jobs", writeFile(t, "jobs.json", jobs)}, &out)
	if err != nil {
		return ciRecord{}, err
	}
	var r ciRecord
	if err := json.Unmarshal([]byte(out.String()), &r); err != nil {
		t.Fatalf("record isn't JSON: %v\n%s", err, out.String())
	}
	return r, nil
}

func TestCIRecord(t *testing.T) {
	r, err := ciRecord1(t, ciRun, ciJobs)
	if err != nil {
		t.Fatal(err)
	}
	if r.SHA != "abc1234def" || r.Subject != "Merge pull request #9" || r.RunID != 42 || r.Attempt != 1 || r.Time != "2026-10-05T10:00:00Z" {
		t.Errorf("identity = %+v", r)
	}
	if r.WallSeconds == nil || *r.WallSeconds != 130 {
		t.Errorf("wall = %v, want 130s: from the run's start to lint, the last job, finishing", r.WallSeconds)
	}
	if len(r.Jobs) != 2 || r.Jobs[0].Name != "lint" || r.Jobs[1].Name != "test" {
		t.Fatalf("jobs = %+v, want lint and test, by name, without the skipped one", r.Jobs)
	}
	test := r.Jobs[1]
	if test.QueuedSeconds != 3 || test.Seconds != 60 {
		t.Errorf("test queued %vs and ran %vs, want 3 and 60", test.QueuedSeconds, test.Seconds)
	}
	var steps []string
	for _, s := range test.Steps {
		steps = append(steps, s.Name)
	}
	if strings.Join(steps, ", ") != "Set up job, Unit tests, Post" || test.Steps[1].Seconds != 54 {
		t.Errorf("steps = %+v, want three, without the skipped one, Unit tests 54s", test.Steps)
	}
	if test.Steps[2].Seconds != 0 {
		t.Errorf("a step ending a second before it started (GitHub's rounding) = %vs, want 0", test.Steps[2].Seconds)
	}
}

// TestCIRecordOfARerun checks a re-run of failed jobs: its jobs keep their
// times, those carried over from the first attempt included, but it has no
// pipeline time, and a carried-over job's wait isn't counted. GitHub gives a
// carried-over job the new attempt's created_at and its own, earlier,
// started_at, as below for test.
func TestCIRecordOfARerun(t *testing.T) {
	run := strings.Replace(ciRun, `"run_attempt": 1`, `"run_attempt": 2`, 1)
	run = strings.Replace(run, `"run_started_at": "2026-10-05T10:00:00Z"`, `"run_started_at": "2026-10-05T10:00:05Z"`, 1)
	r, err := ciRecord1(t, run, ciJobs)
	if err != nil {
		t.Fatal(err)
	}
	if r.WallSeconds != nil {
		t.Errorf("a re-run's wall = %v, want none", *r.WallSeconds)
	}
	lint, test := r.Jobs[0], r.Jobs[1]
	if test.Seconds != 60 || test.QueuedSeconds != 0 {
		t.Errorf("carried-over test: ran %vs, queued %vs; want 60 and 0", test.Seconds, test.QueuedSeconds)
	}
	if lint.Seconds != 120 || lint.QueuedSeconds != 9 {
		t.Errorf("re-run lint: ran %vs, queued %vs; want 120 and 9", lint.Seconds, lint.QueuedSeconds)
	}
}

func TestCIRecordRefuses(t *testing.T) {
	for name, tt := range map[string]struct{ run, jobs string }{
		"a failed run":          {strings.Replace(ciRun, `"success"`, `"failure"`, 1), ciJobs},
		"a cancelled run":       {strings.Replace(ciRun, `"success"`, `"cancelled"`, 1), ciJobs},
		"no jobs":               {ciRun, ""},
		"only skipped jobs":     {ciRun, lastLine(ciJobs)},
		"a bad timestamp":       {ciRun, strings.Replace(ciJobs, "2026-10-05T10:00:04Z", "soon", 1)},
		"a run that isn't JSON": {"{", ciJobs},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ciRecord1(t, tt.run, tt.jobs)
			if err == nil {
				t.Fatal("nil error, want a refusal")
			}
			t.Log(err)
		})
	}
	if err := runCIRecord([]string{"-run", "x.json"}, &strings.Builder{}); err == nil {
		t.Error("ci-record without -jobs = nil error, want one")
	}
}

func TestTrendsChartsCIRecords(t *testing.T) {
	a, err := ciRecord1(t, ciRun, ciJobs)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.SHA, b.Subject, b.WallSeconds = "fff9999aaa", "<b>Next</b>", ptr(180)
	b.Jobs = []ciJob{{Name: "test", Seconds: 90}, {Name: "fuzz", Seconds: 150}} // lint gone, fuzz new
	dir := t.TempDir()
	for name, r := range map[string]ciRecord{"20261005T100000-abc1234.json": a, "20261005T110000-fff9999.json": b} {
		buf, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(dir, name), buf, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	perf := record1(t, "aaa1111", "First", "2026-10-01T10:00:00Z")

	var out strings.Builder
	if err := runTrends([]string{"-records", writeRecords(t, perf), "-ci-records", dir}, &out); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	mustContain(t, page, "CI pipeline time", "CI time per job",
		"abc1234 pipeline: 2.17 minutes", "fff9999 pipeline: 3.00 minutes", // 130s and 180s, oldest first
		"fff9999 fuzz: 2.50 minutes", "abc1234 lint: 2.00 minutes",
		"aaa1111 p99") // the performance charts keep their own commits
	if strings.Contains(page, "<b>Next</b>") {
		t.Error("a commit subject went into the page unescaped")
	}
	if strings.Contains(page, "fff9999 lint") || strings.Contains(page, "abc1234 fuzz") {
		t.Error("a job charted for a run it wasn't in")
	}

	if err := runTrends([]string{"-records", writeRecords(t, perf), "-ci-records", t.TempDir()}, &strings.Builder{}); err == nil {
		t.Error("trends with an empty -ci-records directory = nil error, want one")
	}
}
