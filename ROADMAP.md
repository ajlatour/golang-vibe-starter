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
- **Every request answered, and logged as what the client got.** A deadline inside the server's
  write timeout ([ADR-0041](docs/adr/0041-every-request-has-a-deadline-inside-the-servers-write-timeout.md));
  JSON errors with codes for every request, unknown routes and methods included
  ([ADR-0042](docs/adr/0042-requests-no-route-takes-get-json-errors-too.md)); requests the HTTP
  server rejects itself logged and counted; `write_error` when a response didn't arrive, and
  requests stopped by a shutdown answered
  ([ADR-0043](docs/adr/0043-every-request-the-handler-gets-is-logged-as-what-the-client-got.md));
  background tasks' panics recovered, and a memory limit from the container's
  ([ADR-0044](docs/adr/0044-a-panic-or-memory-pressure-doesnt-take-the-process-down-with-every-request.md)).
- **Observability.** A log contract checked by tests, including which messages carry no
  `request_id`; trace context carried through (W3C `traceparent`); request and runtime metrics;
  validator call metrics; `runtime stats` in the logs.
- **Pipeline.** Unit, race, fuzz, OpenAPI-scenario and Gherkin integration tests; gosec,
  staticcheck and govulncheck; a performance comparison on every pull request with an allocation
  gate; main's performance and CI-time trends, attached to every release with its docs; a nightly
  soak test that fails on heap or goroutine growth; the middleware's order held by a test; every
  image pin kept current by Dependabot.
- **Releases.** Known deployment configurations must still start; breaking API changes are
  flagged on their pull request and need a big enough version bump (`cmd/apidiff`); the image is
  smoke-tested on both platforms, by digest, before it's tagged; changelog entries and decision
  records are a file each, so pull requests don't conflict over them.

## In flight

Nothing queued. What earlier batches built is above, and in the
[changelog](CHANGELOG.md) and the [decision records](docs/decisions.md).

## A starter for new services

What would make this a template a team starts each new Go service from, and has it serving its
first real endpoint the same day. Today, starting one means renaming the template by hand (its
name is in about 230 places), copying `internal/item` (about 3,000 lines with its tests) and
editing the six other places a new route touches, all of which the drift tests check; and the
service can't run without Docker, since its only store is the upstream.

In the order they build on each other: the building blocks first, then what uses them.

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **Shared building blocks for resources.** Move what every resource repeats out of `internal/item` into `httpx`: conditional requests (`ETag`, `If-Match`, `If-None-Match`), cursor paging (`limit`, `cursor`, `next_cursor`), mapping a store's errors to statuses, and decode-then-validate. | A new resource is then its model and its handlers, a few hundred lines, not a copy of `item`'s plumbing. Prerequisite for the generator below. | stdlib |
| 💡 | **Run with no infrastructure.** Promote the in-memory store from `itemtest` to a real adapter, chosen with `STORE=memory`, and a `make dev` that runs the service with it, rebuilding on change. | `go run ./cmd/api` serves requests on a fresh clone, with no Docker, upstream or flagd. The first five minutes decide whether a template gets used. | stdlib; a file watcher via `go run` |
| 💡 | **`make init NAME=orders MODULE=github.com/acme/orders`.** Renames the module, the service, its image and its compose project; empties the changelog and `changes/`; and, optionally, removes the example resource. Plus GitHub's "template repository" setting, and a page saying what to change first. | Renaming by hand misses some of the 230 places. A test would run it on a copy and run the suite, so it can't rot. | stdlib (a Go program in `cmd/`) |
| 💡 | **A resource generator: `make resource NAME=order`.** Writes the package (model, store interface, handlers, in-memory store, contract tests), its paths and schemas in the spec, WireMock stubs, a `docs/curl.md` section, its share of the load test, and an integration feature, all passing the drift tests as generated. | Adding a resource is the most common change, and touches the most places. A test generates one in a copy and runs every check on it. | stdlib (`text/template`) |
| 💡 | **An ADR for dependencies in adapters.** Keep the core (handlers, models, protections) standard library only, and allow vetted dependencies in adapter packages behind interfaces, from a list the architecture test enforces. | A microservice needs a database driver and a telemetry SDK. DEP-1 rules both out, and the rows below need them. | an ADR superseding [ADR-0002](docs/adr/0002-the-service-uses-the-standard-library-only.md) |
| 💡 | **A SQL store**, Postgres, with migrations embedded and run at startup (or as a job), transactions, and the store contract run against a real database in the integration stack. | Most services own their data; the upstream store fits only those that front another service. | [`pgx`](https://github.com/jackc/pgx), in an adapter |
| 💡 | **OpenTelemetry spans and OTLP export**, with a Collector and a trace viewer (Jaeger or Tempo) in the compose stack. | Trace context is already carried and logged; spans show where a request's time went, across services. | the OTel Go SDK, in an adapter |
| 💡 | **Events and a worker.** A transactional outbox, a publisher, and a `cmd/worker` that consumes, built on the same plumbing: configuration, logging, `logx.Go`, graceful shutdown, readiness. | Services talk by events as well as requests; a worker template saves building that plumbing a second time. | a broker client (NATS or Kafka), in an adapter |
| 💡 | **Service-to-service authentication:** verifying JWTs from an OIDC issuer (JWKS fetched and cached), beside API keys. | API keys suit external clients; services usually authenticate with the platform's tokens. | stdlib (`crypto`) |
| 💡 | **A typed Go client for the API**, tested against the spec, published with each release. | Other services call this one; a client that can't drift from the spec saves each of them writing one. | stdlib |
| 💡 | **Extract a versioned platform module** of the generic packages (`httpx`, `logx`, `envconfig`, the protections, `jsoncall`, `tracectx`), once a second service exists. | Fixes, security fixes above all, reach every service from one place; a new service is then its features and its wiring. | a module of its own |
| 💡 | **A Kubernetes deployment**: Kustomize base and overlays with the probes, resources, a restricted security context (non-root, read-only root filesystem, no capabilities), a disruption budget, autoscaling, scraping `/metrics`, and a termination grace period longer than `SHUTDOWN_DELAY` plus `SHUTDOWN_TIMEOUT`. Checked with kubeconform, and smoke-tested in a kind cluster in CI. | Every service needs one, and getting shutdown and probes right is subtle; the settings already exist here to match. | containers in CI |
| 💡 | **Dashboards and alerts per service**: SLOs from the request metrics, and Prometheus alert rules, generated with the service. | A service is operable on its first day. | `monitoring/` |
| 💡 | **One spec file per resource**, bundled into `docs/openapi.json` by the build. | With several resources, one large spec file conflicts as the changelog did. | Redocly's `bundle`, already in the toolchain |

## Resilience

How the service behaves when what it depends on doesn't.

| | Item | Why | Cost |
| - | ---- | --- | ---- |
| 💡 | **Circuit breaker for the upstream.** After repeated failures, fail fast for a cool-off instead of trying every request. | The retry budget limits extra load, but a dead upstream still costs each request a timeout. `internal/outage` already has the state a breaker needs. | stdlib; or [`sony/gobreaker`](https://github.com/sony/gobreaker) as a service dep |
| 💡 | **Deadline budgets, passed on.** Each request has a deadline now (`REQUEST_TIMEOUT`, ADR-0041), and every call it makes is bounded by it; what's left is telling the upstream how long it has (a `Request-Timeout`-style header). | Lets the upstream stop work nobody will read. Needs an upstream that honours it. | stdlib |
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
| 💡 | **A deprecation policy for API versions**, with `Deprecation` and `Sunset` headers. | Clients learn a version is going before it's gone. | stdlib |
