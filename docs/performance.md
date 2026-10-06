# Performance

Three tools, from the cheapest to the most realistic:

- **Go benchmarks** measure what one request costs in-process, layer by layer, with no Docker.
  Use them to compare a change against `main`.
- **k6 load profiles** drive the whole stack in Docker at a fixed request rate and check latency
  and error thresholds: smoke, load, stress, spike and soak.
- **pprof** profiles show where the running service spends its CPU and memory, captured under load.

Alongside them, every load run records what it cost in CPU and memory, and `make monitor` puts
load, containers and the Go runtime on one live dashboard: see
[watching resources](#watching-resources).

| Command | What it does |
| ------- | ------------ |
| `make bench` | Runs the Go benchmarks, 6 times each, into `bin/bench/current.txt` |
| `make bench-compare` | Runs them here and on `main` (`BENCH_BASE=<ref>` for another), interleaved, and compares them with benchstat |
| `make load-compare` | Runs the k6 `compare` profile against this tree and `main`, interleaved |
| `make perf-compare` | Both, written up as the comment CI posts on a pull request: `bin/perf/report.md` |
| `make perf PROFILE=<name>` | Starts the stack and runs a k6 profile (default `smoke`) |
| `make perf-profile PROFILE=<name>` | Runs a k6 profile (default `load`) and captures pprof profiles during and after it |
| `make perf-trends` | Charts `main`'s performance over time, from the records CI keeps: `bin/perf/trends.html` |
| `make monitor` | Starts the stack with Prometheus, Grafana and cAdvisor: a live dashboard at http://localhost:3000 |

Nothing to install beyond Go and Docker: k6 runs from its image, and benchstat through `go run`.

## Go benchmarks

| Benchmark | Measures |
| --------- | -------- |
| `cmd/api` `BenchmarkAPI` | A request through the whole service: middleware, header guard, handler, the upstream call over loopback HTTP to an in-process upstream, and JSON both ways, logging at INFO. One sub-benchmark per route, plus a 404 and a 422 |
| `internal/httpx` `BenchmarkMiddleware` | Correlation ID, the request log lines and panic recovery, with a generated or a caller's ID |
| `internal/headerguard` `BenchmarkMiddleware` | Screening a typical request's headers, and stripping a proxy header |
| `internal/logx` `BenchmarkLogLine` | One request-scoped log line in JSON and text, and one below the level |

`make bench-compare` checks out the base in a temporary worktree and runs both versions'
benchmarks in alternating rounds, then prints benchstat's table: time, bytes and allocations per operation, with the change and
whether it is statistically significant (`~` means it isn't). Benchmarks need a quiet machine:
close what you can, and plug in a laptop. Allocations per operation don't depend on the machine,
so a change in them is real wherever it shows up.

## Load tests (k6)

`perf/k6.js` sends a mix of traffic like a real client's, with the stack's development API key (or
`API_KEY`, to load-test with another): 40% `GET /items`, 40% `GET /items/{id}`,
10% creates, 5% replaces and 5% deletes, against the items WireMock serves. It sends at a **fixed
rate**, starting requests on schedule whether or not earlier ones have answered, so a slow service
can't hide by slowing the test down (the trap called coordinated omission).

Each profile in `perf/profiles.json` is a list of phases (a rate, a duration, and optionally a
ramp from a lower rate) and thresholds. A threshold can apply to one phase, written
`http_req_duration{phase:steady}`, which is how the warm-up stays out of the results.

| Profile | Shape | Passes when |
| ------- | ----- | ----------- |
| `smoke` | 30s at 50 req/s | No errors to speak of, p99 under 500ms |
| `compare` | 20s at 300 req/s | The same as `smoke`. Short, but enough requests for a steady p99: every pull request runs it, against itself and its base |
| `load` | 30s ramp, then 3 minutes at 200 req/s | p99 under 25ms, p99.9 under 100ms, under 0.1% errors |
| `stress` | Steps of 1 minute at 500, 1000, 2000, 4000 and 8000 req/s | Always: see below |
| `spike` | 100 req/s, 30s at 2000 req/s, then 100 req/s again | Under 1% errors in the spike, and back within budget in the recovery phase |
| `soak` | 30 minutes at 200 req/s | The same as `load`, for longer |

k6 prints a summary at the end: each threshold with ✓ or ✗ and the measured value, then latency
percentiles, overall and per phase. The summary is also saved as JSON in `bin/perf/`.

**Stress** looks for the knee: the highest rate the setup handles within budget. Each step has its
own thresholds, so the last step whose thresholds all hold is the answer. Crossing thresholds is
the point, so `make perf PROFILE=stress` succeeds either way.

**Soak** finds what only time shows: memory, goroutines or connections that grow. Run it with
`make perf-profile PROFILE=soak` and compare the heap and goroutine profiles from the start and
the end (below).

### The thresholds

The thresholds in `perf/profiles.json` are starting points, set well above what the local stack
measures (a p99 of about 2ms at 200 req/s on an M-series Mac), so they catch real damage rather
than noise. Once you have baselines from the environment that matters, tighten them there. A test
(`perf/profiles_test.go`) checks every profile's phases and thresholds are well formed, and that
the traffic mix covers every operation in `docs/openapi.json`.

### What the numbers mean locally

The local stack is a measurement of your laptop, not of production:

- The API, WireMock and k6 share the machine's CPUs, and on macOS they all run in Docker's VM.
- WireMock is a JVM. Its JIT warm-up and garbage-collection pauses show up in the API's tail
  latency as upstream time; [load-test tuning](local-stack.md#load-test-tuning) covers this.
- The API logs four lines per request at INFO. That's realistic, but `LOG_LEVEL=warn make perf`
  shows what logging costs.

Use local runs to compare before and after a change, on the same machine, and keep absolute
numbers for an environment shaped like production.

## Watching resources

Three layers, from the lightest. None adds a dependency to the service.

**The API's request metrics.** With `PPROF_ADDR` set, `/metrics` there has every request the API
served (`internal/httpmetrics`): `http_requests_total` by route pattern, method and status,
`http_request_duration_seconds`, a latency histogram by the same labels, and
`http_requests_in_flight`. They're what you'd alert on in production: the rate, errors and
duration of each route. A route is the pattern a request matched, `none` for one answered before
routing (refused by the header guard, authentication or the limits), and an unusual method counts
as `OTHER`, so no request can create series at will. Recording a request allocates nothing.

**The API's runtime metrics.** The same `/metrics` also has every scalar measurement in Go's
`runtime/metrics` (goroutines, heap, allocations, GC cycles, CPU spent in GC, and more), with the
process's CPU time and, on Linux, its resident memory. They're in Prometheus's text format, written
with the standard library alone (`internal/profiling/metrics.go`) and named as the Prometheus Go
client names them, so `go_sched_goroutines_goroutines` or `go_gc_heap_allocs_bytes_total`. Read them
with curl while the stack runs:

```sh
curl -s localhost:6061/metrics | grep -E '^(go_sched_goroutines_goroutines|go_memory_classes_heap_objects_bytes|process_cpu_seconds_total) '
```

**Every load run's cost.** `perf/run.sh`, behind every `make perf`, samples `docker stats` about
once a second during the run and reads the API's `/metrics` just before and just after it
(`perf/resources.sh`). At the end it prints each container's average and peak CPU and peak memory,
and what the API process spent:

```text
Resources during the run (docker stats, about once a second):
  container    CPU avg  CPU peak  memory peak
  api             3.8%      6.4%     15.0 MiB
  flagd           0.8%      1.9%     21.7 MiB
  mock           16.0%     26.5%    330.8 MiB
  k6              3.7%      5.5%     22.1 MiB
  (100% is one CPU core.)
The API process (its /metrics, before and after):
  CPU time 1.15s, allocated 20.3 MiB in 8 GC cycles; at the end 0.9 MiB of heap and 32 goroutines
```

The other rows matter as much as the API's: on one machine, a busy WireMock or k6 can be the real
limit on what the API is asked to do. The raw samples are saved next to the k6 summary in
`bin/perf/`. The pull request report compares the same numbers, base against PR (see
[pull request reports](#pull-request-reports)).

**A live dashboard.** `make monitor` starts the stack with three more containers, under the
`monitoring` compose profile:

| Container | What it does |
| --------- | ------------ |
| cAdvisor | Measures every container's CPU, memory and network |
| Prometheus, `localhost:9090` | Scrapes cAdvisor and the API's `/metrics` every 2s, and receives k6's results as they happen |
| Grafana, `localhost:3000` | Shows the performance dashboard, with no login: it listens on this machine's loopback only |

While the stack is up, `make perf` (any profile) sends k6's results to Prometheus too, so the
dashboard shows k6's requests per second, latency and errors by route, the API's own view of the
same (rate by route, responses by status, p99 by route, requests in flight), each container's CPU,
memory and network, and the API's CPU, heap, allocation rate, GC and goroutines, on one timeline. A soak run
(`make perf PROFILE=soak`) is where it pays off: memory or goroutines that climb and don't come
back down. `make down` stops it all.

The dashboard is `monitoring/grafana/dashboards/performance.json`, loaded read-only: edit the JSON,
or change it in Grafana and export it over the file. `monitoring/monitoring_test.go` checks every
Go runtime metric it plots exists in `/metrics`, and that the k6 percentiles it plots are ones
`perf/run.sh` asks k6 to send. On macOS, cAdvisor runs inside Docker Desktop's Linux VM and sees
the containers there, which is what matters; the Mac itself isn't measured.

## Profiling

The API serves Go's runtime profiles (`net/http/pprof`), and its runtime metrics, when `PPROF_ADDR`
is set, on that address only, never on the API's port. It's off by default. The compose stack sets it and publishes it as
`127.0.0.1:6061`, on this machine's loopback only, since profiles expose the program's internals
(function names, stacks, what holds memory). If you enable it anywhere else, keep the port private
the same way. The API logs a WARN at startup when the listener is reachable from other machines,
which inside a container is expected (the port's publishing decides who can reach it) and anywhere
else a mistake. A CPU profile or trace may run for at most 60 seconds (`?seconds=`), since both
cost CPU for as long as they run.

`make perf-profile` runs a k6 profile and, after a 40s warm-up, captures heap and goroutine
snapshots, a 30s CPU profile and an allocation profile. When the run ends it captures heap and
goroutine snapshots again. Everything goes into `bin/perf/pprof-<time>/`, and the target prints the
commands to explore it:

```sh
go tool pprof -http=: bin/perf/pprof-<time>/cpu.pprof
go tool pprof -http=: -diff_base=bin/perf/pprof-<time>/heap-start.pprof bin/perf/pprof-<time>/heap-end.pprof
```

The first opens the CPU profile in the browser: a flame graph under View. The second shows what
the end of the run holds beyond the snapshot under load, which is where a leak shows. To grab one
profile by hand while the stack runs:

```sh
curl -o cpu.pprof 'localhost:6061/debug/pprof/profile?seconds=30'
curl 'localhost:6061/debug/pprof/goroutine?debug=1'
```

The block and mutex profiles are served but empty: they need sampling switched on in code
(`runtime.SetBlockProfileRate`, `runtime.SetMutexProfileFraction`), which costs throughput. Turn
them on locally when chasing contention.

## Pull request reports

Every pull request gets a performance comparison with its base branch, from the Performance
workflow (`.github/workflows/perf.yml`), kept in a single comment that's edited on every push. Its
edit history shows how the numbers moved across pushes.

**How it's measured.** Shared CI runners differ from one another by 10% or more, so absolute numbers
from them mean little. So each comparison measures both versions on one runner, interleaved:

- **Benchmarks** (`perf/bench-ab.sh`): 10 rounds of base then PR, or PR then base, each benchmark
  running for 300ms a round. Many short samples serve the statistics better than a few long ones,
  and allocation counts are exact however long a benchmark runs.
- **Load** (`perf/load-ab.sh`): the k6 `compare` profile in 2 rounds, against an API image built
  from each version, after a warm-up run that's thrown away.

Whatever slows a runner meanwhile lands on both versions alike. The two comparisons run as parallel
jobs on separate runners, and a third job writes the report from their results. None of them can
write to the pull request: the report tool is built from the pull request's own code, so a fourth
job posts the comment and applies the verdict, reading the report as data and never checking out
or running the pull request's code. Changes to Markdown and `docs/` alone don't run the workflow.

**What the comment shows.** First the verdict, then the changes that matter, then the load test's
latency and errors for each version, and what the API cost while it ran: CPU per request (from
its own CPU time, so it doesn't depend on how busy the runner was), its container's average CPU
and peak memory, and bytes allocated and GC cycles per request. Every benchmark is in a collapsed table, with full benchstat
output and k6's output in the run, which the comment links to.

| Change | Effect |
| ------ | ------ |
| More allocations per operation on any benchmark | ❌ **Fails the check.** Allocation counts are exact, the same on every run and machine, so any increase is a real change. If it's intended, add the `perf-ok` label to the pull request; the workflow reruns and accepts it |
| The load test crosses the `compare` profile's thresholds | ❌ **Fails the check.** Errors, or a p99 far over budget: the service is broken. No label accepts this |
| Time per operation up more than 15%, or bytes per operation up more than 10%, and statistically significant | ⚠️ Reported, never failed on |
| p99 under load up more than 25% and more than 1ms | ⚠️ Reported, never failed on |
| CPU per request up more than 25%, or peak memory up more than 25% and 10 MiB | ⚠️ Reported, never failed on |
| Anything smaller, or not significant (`~`) | Shown in the full table only |

A ⚠️ on a shared runner is a lead, not a verdict: confirm it on a quiet machine with
`make perf-compare`, which runs the same comparison against `main` (`BENCH_BASE=<ref>` for another)
and writes the same report to `bin/perf/report.md`. It takes about 3 minutes; `make bench-compare`
and `make load-compare` run its two halves.

A pull request from a fork gets a read-only token, so it can't be commented on; its report is in
the workflow run's summary instead.

The report is written by `perf/report`, a small Go module of its own (like `integration/`, so the
service stays standard library only). It uses benchstat's statistics library, so it agrees with
`make bench-compare`.

## Trends

The pull request report says whether one change made things worse than its base. A slow drift,
a little worse with each merge, never shows there. So after every merge, the Performance trends
workflow (`.github/workflows/perf-trends.yml`) measures `main` alone: the Go benchmarks (6 runs
each) and the `compare` load profile (2 runs, with what they cost in CPU and memory). It saves the
results as one small JSON file per commit on the `perf-data` branch, so the history lives in git.
Only `main`'s own code runs in that job, and it's the only one that writes to the repository, and
only to that branch.

`make perf-trends` fetches the branch and writes `bin/perf/trends.html`: charts of latency under
load, CPU and allocations per request, the API container's peak memory, and the request path's
benchmarks (allocations and time per route), one point per commit. Hover a point for its commit.
The page is drawn by the report tool, with no JavaScript or chart library.

The timings come from shared runners, so a single point means little; look for steps and slopes.
Allocations are exact, so a step in them is always a real change.

## Pipeline time

How long CI takes is tracked the same way. After every successful CI run on `main`, the CI times
workflow (`.github/workflows/ci-times.yml`) reads the run from the GitHub API and saves one JSON
file per run under `ci/` on the `perf-data` branch, with:

- the pipeline's time, from the run starting to its last job finishing, which is how long a merge
  waits for its checks;
- each job's running time, and how long it waited for a runner;
- each step's time, for working out what made a job slower.

`make perf-trends` charts the pipeline's time and each job's beside the performance charts, once
there are records. The jobs run in parallel, so the slowest one, usually `fuzz` or `lint`, sets
the pipeline's time.

It runs after CI rather than inside it, so it sees every job's finished timings, and a failure in
it never fails CI. Only successful runs are recorded: a failed or cancelled run stopped early and
would read as a faster pipeline. A re-run of failed jobs keeps its jobs' times but has no pipeline
time, because the jobs it carried over ran before it started. Like the other timings, these come
from shared runners, so look for steps and slopes rather than single points.
