# Performance

Three tools, from the cheapest to the most realistic:

- **Go benchmarks** measure what one request costs in-process, layer by layer, with no Docker.
  Use them to compare a change against `main`.
- **k6 load profiles** drive the whole stack in Docker at a fixed request rate and check latency
  and error thresholds: smoke, load, stress, spike and soak.
- **pprof** profiles show where the running service spends its CPU and memory, captured under load.

| Command | What it does |
| ------- | ------------ |
| `make bench` | Runs the Go benchmarks, 6 times each, into `bin/bench/current.txt` |
| `make bench-compare` | Runs them here and on `main` (`BENCH_BASE=<ref>` for another), interleaved, and compares them with benchstat |
| `make load-compare` | Runs the k6 `compare` profile against this tree and `main`, interleaved |
| `make perf-compare` | Both, written up as the comment CI posts on a pull request: `bin/perf/report.md` |
| `make perf PROFILE=<name>` | Starts the stack and runs a k6 profile (default `smoke`) |
| `make perf-profile PROFILE=<name>` | Runs a k6 profile (default `load`) and captures pprof profiles during and after it |

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

`perf/k6.js` sends a mix of traffic like a real client's: 40% `GET /items`, 40% `GET /items/{id}`,
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

## Profiling

The API serves Go's runtime profiles (`net/http/pprof`) when `PPROF_ADDR` is set, on that address
only, never on the API's port. It's off by default. The compose stack sets it and publishes it as
`127.0.0.1:6061`, on this machine's loopback only, since profiles expose the program's internals
(function names, stacks, what holds memory). If you enable it anywhere else, keep the port private
the same way.

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
jobs on separate runners, and a third job writes the report from their results; only that job can
write to the pull request. Changes to Markdown and `docs/` alone don't run the workflow.

**What the comment shows.** First the verdict, then the changes that matter, then the load test's
latency and errors for each version. Every benchmark is in a collapsed table, with full benchstat
output and k6's output in the run, which the comment links to.

| Change | Effect |
| ------ | ------ |
| More allocations per operation on any benchmark | ❌ **Fails the check.** Allocation counts are exact, the same on every run and machine, so any increase is a real change. If it's intended, add the `perf-ok` label to the pull request; the workflow reruns and accepts it |
| The load test crosses the `compare` profile's thresholds | ❌ **Fails the check.** Errors, or a p99 far over budget: the service is broken. No label accepts this |
| Time per operation up more than 15%, or bytes per operation up more than 10%, and statistically significant | ⚠️ Reported, never failed on |
| p99 under load up more than 25% and more than 1ms | ⚠️ Reported, never failed on |
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
