package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"time"
)

// A ciRecord is how long one CI run on main took, and each of its jobs and
// their steps, as the ci-times workflow reads them from the GitHub API after
// every successful run. A directory of them is the history `report trends
// -ci-records` charts beside the performance records.
type ciRecord struct {
	SHA     string `json:"sha"`
	Time    string `json:"time"` // when the run started, RFC 3339
	Subject string `json:"subject"`
	RunID   int64  `json:"run_id"`
	Attempt int    `json:"attempt"`
	// WallSeconds is from the run starting to its last job finishing: what
	// a merge waits for. Nil for a re-run (Attempt over 1): it re-ran only
	// some jobs, and the rest, carried over from the earlier attempt, ran
	// before it started, so neither its span nor the whole run's (which
	// includes however long until someone re-ran it) is the pipeline's time.
	WallSeconds *float64 `json:"wall_seconds,omitempty"`
	Jobs        []ciJob  `json:"jobs"`
}

type ciJob struct {
	Name string `json:"name"`
	// QueuedSeconds is how long the job waited for a runner, and Seconds how
	// long it then ran. A job a re-run carried over from an earlier attempt
	// keeps its time, but its wait isn't known: zero.
	QueuedSeconds float64  `json:"queued_seconds"`
	Seconds       float64  `json:"seconds"`
	Steps         []ciStep `json:"steps"`
}

type ciStep struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
}

// apiRun is the part of a workflow run (the workflow_run event's payload, or
// GET /repos/{owner}/{repo}/actions/runs/{id}) a record needs.
type apiRun struct {
	ID           int64  `json:"id"`
	HeadSHA      string `json:"head_sha"`
	RunAttempt   int    `json:"run_attempt"`
	RunStartedAt string `json:"run_started_at"`
	Conclusion   string `json:"conclusion"`
	HeadCommit   struct {
		Message string `json:"message"`
	} `json:"head_commit"`
}

// apiJob is the part of a job, from GET .../runs/{id}/attempts/{n}/jobs, a
// record needs.
type apiJob struct {
	Name        string    `json:"name"`
	Conclusion  string    `json:"conclusion"`
	CreatedAt   string    `json:"created_at"`
	StartedAt   string    `json:"started_at"`
	CompletedAt string    `json:"completed_at"`
	Steps       []apiStep `json:"steps"`
}

type apiStep struct {
	Name        string `json:"name"`
	Conclusion  string `json:"conclusion"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
}

// runCIRecord is `report ci-record`: a CI run's timings as a record.
func runCIRecord(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ci-record", flag.ContinueOnError)
	runFile := fs.String("run", "", "the workflow run, as JSON")
	jobsFile := fs.String("jobs", "", "the run's jobs, one JSON object each (gh api --paginate --jq '.jobs[]')")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runFile == "" || *jobsFile == "" {
		return errors.New("ci-record needs -run and -jobs")
	}
	var run apiRun
	if err := readJSON(*runFile, &run); err != nil {
		return err
	}
	jobs, err := readJobs(*jobsFile)
	if err != nil {
		return err
	}
	r, err := newCIRecord(run, jobs)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// readJobs reads a stream of job objects.
func readJobs(path string) ([]apiJob, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var jobs []apiJob
	dec := json.NewDecoder(f)
	for {
		var j apiJob
		if err := dec.Decode(&j); err == io.EOF {
			return jobs, nil
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		jobs = append(jobs, j)
	}
}

// newCIRecord makes the record of a successful run. A run that failed or was
// cancelled stopped early, so its times would read as a faster pipeline;
// it's refused.
func newCIRecord(run apiRun, jobs []apiJob) (ciRecord, error) {
	if run.Conclusion != "success" {
		return ciRecord{}, fmt.Errorf("run %d concluded %q: only successful runs are recorded", run.ID, run.Conclusion)
	}
	if len(jobs) == 0 {
		return ciRecord{}, fmt.Errorf("run %d: no jobs", run.ID)
	}
	start, err := time.Parse(time.RFC3339, run.RunStartedAt)
	if err != nil {
		return ciRecord{}, fmt.Errorf("run %d: run_started_at: %w", run.ID, err)
	}
	subject, _, _ := strings.Cut(run.HeadCommit.Message, "\n")
	r := ciRecord{SHA: run.HeadSHA, Time: run.RunStartedAt, Subject: subject, RunID: run.ID, Attempt: run.RunAttempt}
	var end time.Time
	for _, j := range jobs {
		if j.Conclusion == "skipped" {
			continue
		}
		created, err1 := time.Parse(time.RFC3339, j.CreatedAt)
		started, err2 := time.Parse(time.RFC3339, j.StartedAt)
		completed, err3 := time.Parse(time.RFC3339, j.CompletedAt)
		if err := errors.Join(err1, err2, err3); err != nil {
			return ciRecord{}, fmt.Errorf("job %q: %w", j.Name, err)
		}
		job := ciJob{Name: j.Name, Seconds: seconds(completed.Sub(started))}
		if !started.Before(start) { // not carried over from an earlier attempt
			job.QueuedSeconds = seconds(started.Sub(created))
		}
		for _, s := range j.Steps {
			if s.Conclusion == "skipped" {
				continue
			}
			ss, err1 := time.Parse(time.RFC3339, s.StartedAt)
			sc, err2 := time.Parse(time.RFC3339, s.CompletedAt)
			if err := errors.Join(err1, err2); err != nil {
				return ciRecord{}, fmt.Errorf("job %q, step %q: %w", j.Name, s.Name, err)
			}
			job.Steps = append(job.Steps, ciStep{Name: s.Name, Seconds: seconds(sc.Sub(ss))})
		}
		r.Jobs = append(r.Jobs, job)
		if completed.After(end) {
			end = completed
		}
	}
	if len(r.Jobs) == 0 {
		return ciRecord{}, fmt.Errorf("run %d: every job was skipped", run.ID)
	}
	slices.SortFunc(r.Jobs, func(a, b ciJob) int { return strings.Compare(a.Name, b.Name) })
	if run.RunAttempt <= 1 {
		r.WallSeconds = new(seconds(end.Sub(start)))
	}
	return r, nil
}

// seconds is d in seconds, never negative: GitHub's timestamps are to the
// second, so a step can appear to end before it starts.
func seconds(d time.Duration) float64 {
	return math.Max(0, d.Seconds())
}

// readCIRecords reads every CI record in dir, oldest first by file name,
// which the workflow starts with the time.
func readCIRecords(dir string) ([]ciRecord, error) {
	var out []ciRecord
	err := readDir(dir, func(path string, b []byte) error {
		var r ciRecord
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

// ciCharts charts the CI records: the whole run's wall time, and each job's.
// Their x axis is the CI records, which aren't the performance records' commits:
// CI runs on every merge, the performance trends only on those that can change
// performance.
func ciCharts(recs []ciRecord) []chart {
	points := make([]record, len(recs))
	for i, r := range recs {
		points[i] = record{SHA: r.SHA, Time: r.Time, Subject: r.Subject}
	}
	wall := series{name: "pipeline"}
	var names []string
	for _, r := range recs {
		v := math.NaN() // a re-run's
		if r.WallSeconds != nil {
			v = *r.WallSeconds / 60
		}
		wall.values = append(wall.values, v)
		for _, j := range r.Jobs {
			if !slices.Contains(names, j.Name) {
				names = append(names, j.Name)
			}
		}
	}
	slices.Sort(names)
	var jobs []series
	for _, n := range names {
		s := series{name: n}
		for _, r := range recs {
			v := math.NaN()
			for _, j := range r.Jobs {
				if j.Name == n {
					v = j.Seconds / 60
				}
			}
			s.values = append(s.values, v)
		}
		jobs = append(jobs, s)
	}
	return []chart{
		{title: "CI pipeline time", unit: "minutes", points: points,
			note:   "From the CI run starting to its last job finishing, waits for runners included: how long a merge to main waits for its checks. Successful runs only; a re-run of failed jobs has no pipeline time.",
			series: []series{wall}},
		{title: "CI time per job", unit: "minutes", points: points,
			note:   "Each job's own running time, not counting its wait for a runner. The jobs run in parallel, so the slowest sets the pipeline's time.",
			series: jobs},
	}
}
