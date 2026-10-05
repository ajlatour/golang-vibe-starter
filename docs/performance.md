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
| `make bench-compare` | The same, plus on `main` (`BENCH_BASE=<ref>` for another), compared with benchstat |
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

`make bench-compare` checks out the base in a temporary worktree, runs the same benchmarks there,
and prints benchstat's table: time, bytes and allocations per operation, with the change and
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
| `smoke` | 30s at 50 req/s | No errors to speak of, p99 under 500ms. CI runs it on every pull request |
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

## In CI

Two jobs run on every pull request, and neither fails on numbers alone, because shared runners
vary too much from run to run:

- **load-smoke** runs the `smoke` profile against the stack. It fails on errors or an absurd p99:
  a broken or deadlocked service.
- **bench** runs `make bench-compare` against the pull request's base branch and writes the
  benchstat table to the job's summary page. It fails only if a benchmark can't run. Read the
  allocation columns first: they're exact.
