# Roadmap

What would make this service more resilient and reliable, and its pipeline faster and more
consistent: what's done, what's queued, and what's proposed, with the libraries and tools worth
considering. It's a menu, not a commitment: each item is taken up, or not, by a pull request and,
where it's a decision, an [ADR](docs/decisions.md).

## How to read it

**Status:** ✅ done · 🔄 in progress · ⏭️ queued (decided, not yet built) · 💡 proposed.

**Dependency cost**, because the service uses the standard library only (DEP-1,
[ADR-0002](docs/adr/0002-the-service-uses-the-standard-library-only.md), enforced by a
test):

| Cost | Meaning |
| ---- | ------- |
| **stdlib** | Code in this module, no new dependency. Fits as is. |
| **container** | A tool run in Docker, in the compose stack or CI, like WireMock and Redocly ([ADR-0011](docs/adr/0011-tooling-runs-in-docker-or-through-go-run.md)). No change to the service. |
| **tool module** | A Go dependency in a module of its own (like `integration/` and `perf/report/`), or run with `go run`: tests and tooling only (DEP-2). |
| **service dep** | A dependency of the service itself. Needs an ADR that replaces DEP-1 first. |

## Already in place

For context: what the items below build on.

- **Request path.** Header screening, hashed API keys with rotation, in-flight cap, per-client
  rate limits, request validation (deployable dark), structured errors with stable codes, API
  versioning by header, cursor paging.
- **Dependencies.** Timeouts and a dedicated connection pool for each; outage tracking with
  back-off (`internal/outage`); upstream GET retries within a budget; feature flags with a cache
  and embedded defaults; readiness that tracks reachability.
- **Observability.** A log contract checked by tests; trace context carried through (W3C
  `traceparent`); request and runtime metrics; validator call metrics; `runtime stats` in the logs.
- **Pipeline.** Unit, race, fuzz, OpenAPI-scenario and Gherkin integration tests; gosec,
  staticcheck and govulncheck; a performance comparison on every pull request with an allocation
  gate; main's performance and CI-time trends, attached to every release with its docs; a nightly
  soak test that fails on heap or goroutine growth.

## In flight: the improvement queue

| | Item | Notes |
| - | ---- | ----- |
| ✅ | Stable error codes (ADR-0033) | |
| ✅ | Handlers that return errors (`httpx.Handle`) | |
| ✅ | One outage tracker (`internal/outage`) | |
| ✅ | W3C trace context (ADR-0034) | |
| ✅ | Upstream GET retries within a budget (ADR-0035) | |
| ✅ | API versioning by header (ADR-0036) | |
| ✅ | Cursor paging, 50 by default (ADR-0037) | |
| ✅ | Bound the rate limiter's memory; key IPv6 by /64 (ADR-0038) | |
| ✅ | Check what the upstream sends back (ADR-0039) | items that break the model's rules become a 502 |
| ✅ | `ETag` / `If-Match` for safe concurrent updates (ADR-0040) | |
| ✅ | Nightly soak test, failing on heap or goroutine growth | `.github/workflows/soak.yml` |
| ✅ | Check the WireMock stubs against the `/items` contract | `cmd/api/openapi_stubs_test.go`; see contract testing below |

## Resilience

How the service behaves when what it depends on doesn't.

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **Circuit breaker for the upstream.** After repeated failures, fail fast for a cool-off instead of trying every request. | The retry budget limits extra load, but a dead upstream still costs each request a timeout. `internal/outage` already has the state a breaker needs. | stdlib; or [`sony/gobreaker`](https://github.com/sony/gobreaker) as a service dep |
| 💡 | **Deadline budgets.** Give each call the time the request has left, not a fixed timeout, and pass it on (`Request-Timeout`-style header or gRPC-like deadline). | A 5s upstream timeout inside a 3s client deadline wastes work nobody will read. | stdlib (`context.WithDeadline` already flows) |
| 💡 | **Bulkheads.** A separate in-flight cap per dependency (upstream, validator, flagd). | Today one slow dependency can hold every one of the `MAX_IN_FLIGHT` slots, starving requests that don't need it. | stdlib (a semaphore per client) |
| 💡 | **Adaptive concurrency limits.** Learn the cap from latency (Gradient2, Vegas) instead of a fixed `MAX_IN_FLIGHT`. | A fixed cap is right for one machine and load; adaptive limits track the real capacity. | [`platinummonkey/go-concurrency-limits`](https://github.com/platinummonkey/go-concurrency-limits) as a service dep; or a small stdlib AIMD limiter |
| 💡 | **Serve stale on failure.** Keep the last good answer for idempotent GETs and serve it, marked, when the upstream fails. | Turns an upstream outage into slightly stale data for reads. | stdlib (a bounded LRU) |
| 💡 | **Request coalescing.** Concurrent identical GETs share one upstream call. | Protects the upstream from thundering herds after a cache miss or a restart. | `golang.org/x/sync/singleflight` (service dep), or ~40 lines of stdlib |
| 💡 | **Idempotency keys for writes.** Honour an `Idempotency-Key` header on `POST`, returning the first result for a repeat. | Lets clients retry writes safely, which retries in `httpstore` deliberately don't do. | stdlib, plus storage for the keys |
| 💡 | **A decision cache for request validation.** Cache allow decisions briefly, if the real service's rules permit. | Removes most of the per-request cost the performance reports measure (about 0.4ms and 7 KiB a request locally). | stdlib |
| 💡 | **Priority load shedding.** Under pressure, shed reads before writes, and never the probes. | Keeps the most valuable work going when the service must refuse some. | stdlib |
| 💡 | **Graceful startup.** Warm connection pools and the flag cache before reporting ready. | Avoids a burst of slow first requests after every deploy. | stdlib |

## Reliability and correctness

Catching what tests don't yet.

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **Consumer-driven contract tests** with the real upstream, when there is one. | WireMock stubs encode what we believe the upstream does; a contract both sides verify catches drift before production. | [Pact](https://docs.pact.io) ([`pact-go`](https://github.com/pact-foundation/pact-go)) as a tool module, or a recorded-contract test in stdlib |
| 💡 | **Property-based tests** for paging, cursors, and the rate limiter. | Invariants ("every item is listed exactly once, whatever changes between pages") are stronger than examples. | [`pgregory.net/rapid`](https://github.com/flyingmutant/rapid) as a tool module; or `testing/quick` (stdlib) |
| 💡 | **Fuzzing the API against its spec.** Generate requests from `docs/openapi.json` and check every response conforms. | Finds the inputs nobody thought of, across every route at once. | [Schemathesis](https://schemathesis.io) as a container |
| 💡 | **Fault injection between the API and its dependencies:** latency, resets, partitions, bandwidth limits. | WireMock faults cover answers; a proxy covers the network itself, both ways. | [Toxiproxy](https://github.com/Shopify/toxiproxy) as a container in the compose stack |
| 💡 | **Goroutine leak checks in tests.** Fail a test that leaves goroutines running. | Goroutine leaks are Go's most common memory leak, and `runtime stats` only shows them in production. | [`go.uber.org/goleak`](https://github.com/uber-go/goleak) in a test-only module, or a stdlib `runtime.NumGoroutine` check |
| 💡 | **Mutation testing.** Change the code in small ways and check a test fails. | Measures whether tests would catch real bugs, as the hand-made mutation checks in recent pull requests did. | [`gremlins`](https://github.com/go-gremlins/gremlins) via `go run`, nightly |
| 💡 | **Keep the fuzz corpus** between CI runs. | Each run starts from what earlier runs found, instead of from the seeds. | CI cache |
| 💡 | **Shuffled test order** (`go test -shuffle=on`). | Catches tests that pass only in their usual order. | stdlib flag |

## Observability

Seeing what the service does in production, where the logs are all there is today.

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **OpenTelemetry, collector first.** Run an OTel Collector that scrapes `/metrics` and reads the JSON logs; add the Go SDK later. | Gets metrics and logs into one backend with no dependency; traces already carry `traceparent`. | container; the SDK is a service dep needing an ADR |
| 💡 | **SLOs and error-budget alerts.** Availability and latency objectives per route, with burn-rate alerts. | Alerts on what users feel, not on every 5xx. | [Sloth](https://sloth.dev) (generates Prometheus rules) as a container; rules in `monitoring/` |
| 💡 | **Continuous profiling.** Collect CPU, heap and goroutine profiles from `PPROF_ADDR` every few minutes. | Diagnoses a leak or a slowdown without access to the container. | [Grafana Pyroscope](https://grafana.com/oss/pyroscope/) or [Parca](https://www.parca.dev) as a container |
| 💡 | **Log-based alerts** on `service failed`, sustained `runtime stats` growth, and outage warnings. | Works today, with logs alone. | log platform configuration |
| 💡 | **Exemplars:** link a latency bucket to a trace ID. | Jump from a slow percentile straight to an example request. | stdlib, once traces are recorded |
| 💡 | **`RateLimit` response headers** (IETF draft: `RateLimit-Limit`, `-Remaining`, `-Reset`). | Clients can slow down before they're refused. | stdlib |

## Security

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **Sign the release image** with cosign and keyless OIDC. | Provenance says how the image was built; a signature says who vouches for it (a gap noted in [Releasing](docs/releasing.md#not-yet)). | [Sigstore cosign](https://github.com/sigstore/cosign) in CI |
| 💡 | **Scan the image** for vulnerable OS packages and binaries. | govulncheck covers Go code; the distroless base has its own. | [Trivy](https://trivy.dev) or [Grype](https://github.com/anchore/grype) as a container in CI |
| 💡 | **Secret scanning** on every push and pull request. | Catches a key or token before it lands in history. | [gitleaks](https://github.com/gitleaks/gitleaks) as a container in CI |
| 💡 | **OpenSSF Scorecard.** | Checks the repository's supply-chain hygiene continuously. | [`ossf/scorecard-action`](https://github.com/ossf/scorecard-action), pinned |
| 💡 | **Validate request bodies against the spec at runtime.** | One source of truth for input rules, beyond the tests that check them today. | [`kin-openapi`](https://github.com/getkin/kin-openapi) as a service dep (needs an ADR); today's hand-written rules are tested against the spec instead |
| 💡 | **API key expiry.** An optional expiry date per key, and a warning before it. | Rotation is possible today; expiry makes it happen. | stdlib |
| 💡 | **mTLS to dependencies,** when they're across a network. | Authenticates the upstream and the validation service, and encrypts the hop. | stdlib (`crypto/tls`) |

## Pipeline speed and consistency

A pull request's checks take about 2–3 minutes, and `main`'s CI time is charted per job. Fast
matters, but consistent matters more: a flaky check costs more than a slow one.

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **Make tool downloads reliable.** Cache the modules that `go run tool@version` fetches (gosec, staticcheck, govulncheck, benchstat), or pin them with Go's `tool` directive in a tools module. | A lint job once failed because proxy.golang.org dropped a download; cached tools never reach the network. | CI cache; tool module |
| 💡 | **Run only what a change affects.** Skip the integration and load jobs when only docs change, and run unit tests for changed packages and their dependents (`go list -deps`). | Most pull requests touch a few packages. | CI configuration |
| 💡 | **Docker layer caching** for the API image (deferred earlier). | The integration, load and release jobs each build the image. | [`docker/build-push-action`](https://github.com/docker/build-push-action) with the GitHub Actions cache |
| 💡 | **A merge queue.** | Tests each pull request against the `main` it will land on, so `main` stays green when pull requests land close together. | GitHub merge queue |
| 💡 | **Flaky-test reporting, never skipping.** Rerun failures once to report them as flaky, while still failing the job. | Makes flakes visible and fixable instead of re-run until green. | [`gotestsum --rerun-fails`](https://github.com/gotestyourself/gotestsum) via `go run` |
| 💡 | **A pipeline time budget.** Warn when a job's time, from the CI-time trends, grows past a threshold. | Catches a slow drift, like the fuzz job, before it doubles the pipeline. | stdlib (the perf report tool) |
| 💡 | **Split the fuzz job** across runners, or fuzz longer nightly and briefly on pull requests. | Fuzzing sets the pipeline's time today (about 2 minutes). | CI configuration |
| 💡 | **Grouped dependency updates.** | One pull request a week for related updates, instead of one each. | Dependabot groups, or [Renovate](https://docs.renovatebot.com) |
| 💡 | **Branch protection** requiring the checks, and signed commits. | Makes "green before merge" a rule, not a habit. | repository settings |

## Developer experience

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **Pre-commit hooks** for formatting, vet and the fast tests. | Catches what CI would, before the push. | [lefthook](https://github.com/evilmartians/lefthook) |
| 💡 | **A dev container.** | One command to a working environment, with Go, Docker and the tools. | `.devcontainer/` |
| 💡 | **Readable test output.** | Failures are easier to find in long runs. | [`gotestsum`](https://github.com/gotestyourself/gotestsum) via `go run` |

## When a real domain arrives

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **Domain, application and infrastructure layers** ([ADR-0015](docs/adr/0015-the-domain-driven-restructure-is-deferred.md), [ADR-0030](docs/adr/0030-the-item-model-enforces-its-rules-and-wire-formats-stay-at-the-edges.md)). | Rules beyond CRUD need a place to live. | stdlib |
| 💡 | **Extract a versioned `kit` module** of the generic packages (`httpx`, `logx`, the protections) once a second service exists. | Fixes, security fixes above all, reach every service from one place. | a module of its own |
| 💡 | **A deprecation policy for API versions**, with `Deprecation` and `Sunset` headers. | Clients learn a version is going before it's gone. | stdlib |
