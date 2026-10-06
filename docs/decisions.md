# Architecture decisions

The decisions that shaped this service, one record each: what forced a choice, what was chosen
and what was turned down, and what follows from it. [Architecture](architecture.md) describes the
result; these records say why it is that way.

Most were recorded after they were made: their date is that of the commit that made them, which
is the first place to look for the details.

## How to use these records

- **Before changing something a record covers**, read it: the constraint that forced the choice
  may still hold.
- **To change a decision**, add a new record that supersedes the old one. Set the old record's
  status to "Superseded by ADR-NNNN", and change nothing else in it: the history is the point.
- **To record a new decision**, copy the template below, take the next number, and add it to the
  index. Record it in the pull request that makes the change.

`docs/architecture_test.go` checks every record has a status, context, decision and consequences,
and that the index matches the records.

Template:

```text
## ADR-NNNN: <the decision, as a short statement>

- **Status:** Proposed | Accepted | Superseded by ADR-NNNN
- **Date:** YYYY-MM-DD (commit)

**Context.** What forces a choice: the problem, the constraints, the options considered.

**Decision.** What was chosen, in a sentence or two.

**Consequences.** What follows, good and bad, and what holds it in place.
```

## Index

| ADR | Decision | Status |
| --- | -------- | ------ |
| [ADR-0001](#adr-0001-record-architecture-decisions) | Record architecture decisions | Accepted |
| [ADR-0002](#adr-0002-the-service-uses-the-standard-library-only) | The service uses the standard library only | Accepted |
| [ADR-0003](#adr-0003-items-live-in-an-upstream-service-behind-a-store-interface) | Items live in an upstream service, behind a store interface | Accepted |
| [ADR-0004](#adr-0004-wiremock-is-the-local-upstream) | WireMock is the local upstream | Accepted |
| [ADR-0005](#adr-0005-feature-flags-come-from-flagd-over-ofrep-with-embedded-defaults) | Feature flags come from flagd over OFREP, with embedded defaults | Accepted |
| [ADR-0006](#adr-0006-logs-are-structured-and-held-to-a-contract) | Logs are structured and held to a contract | Accepted |
| [ADR-0007](#adr-0007-every-outbound-call-goes-through-one-json-call) | Every outbound call goes through one JSON call | Accepted |
| [ADR-0008](#adr-0008-the-openapi-spec-is-the-contract-checked-against-the-handlers) | The OpenAPI spec is the contract, checked against the handlers | Accepted |
| [ADR-0009](#adr-0009-integration-tests-run-gherkin-against-the-composed-stack) | Integration tests run Gherkin against the composed stack | Accepted |
| [ADR-0010](#adr-0010-configuration-is-environment-variables-documented-and-checked) | Configuration is environment variables, documented and checked | Accepted |
| [ADR-0011](#adr-0011-tooling-runs-in-docker-or-through-go-run) | Tooling runs in Docker or through go run | Accepted |
| [ADR-0012](#adr-0012-request-headers-are-screened-before-the-handlers) | Request headers are screened before the handlers | Accepted |
| [ADR-0013](#adr-0013-pull-requests-are-compared-with-their-base-on-one-runner) | Pull requests are compared with their base on one runner | Accepted |
| [ADR-0014](#adr-0014-resource-monitoring-adds-containers-not-dependencies) | Resource monitoring adds containers, not dependencies | Accepted |
| [ADR-0015](#adr-0015-the-domain-driven-restructure-is-deferred) | The domain-driven restructure is deferred | Accepted |
| [ADR-0016](#adr-0016-clients-authenticate-with-hashed-api-keys) | Clients authenticate with hashed API keys | Accepted |
| [ADR-0017](#adr-0017-item-ids-are-checked-against-an-allowlist) | Item IDs are checked against an allowlist | Accepted |
| [ADR-0018](#adr-0018-the-service-sheds-load-itself) | The service sheds load itself | Accepted |
| [ADR-0019](#adr-0019-outbound-calls-never-follow-redirects) | Outbound calls never follow redirects | Accepted |
| [ADR-0020](#adr-0020-ci-pins-updates-scans-and-isolates) | CI pins, updates, scans and isolates | Accepted |
| [ADR-0021](#adr-0021-readiness-means-the-upstream-can-be-reached) | Readiness means the upstream can be reached | Accepted |
| [ADR-0022](#adr-0022-metrics-are-prometheus-text-served-privately) | Metrics are Prometheus text, served privately | Accepted |
| [ADR-0023](#adr-0023-a-requests-log-lines-share-state) | A request's log lines share state | Accepted |
| [ADR-0024](#adr-0024-mains-performance-history-lives-on-a-data-branch) | Main's performance history lives on a data branch | Accepted |
| [ADR-0025](#adr-0025-ci-keeps-gos-build-cache-not-dockers-layers) | CI keeps Go's build cache, not Docker's layers | Accepted |
| [ADR-0026](#adr-0026-static-analysis-is-gosec-and-staticcheck-not-codeql) | Static analysis is gosec and staticcheck, not CodeQL | Accepted |
| [ADR-0027](#adr-0027-a-release-is-a-tag-on-main-published-by-ci) | A release is a tag on main, published by CI | Accepted |
| [ADR-0028](#adr-0028-api-keys-can-be-rotated-from-a-file-without-a-restart) | API keys can be rotated from a file, without a restart | Accepted |
| [ADR-0029](#adr-0029-flag-values-are-cached-and-a-flagd-outage-is-retried-once-per-ttl) | Flag values are cached, and a flagd outage is retried once per TTL | Accepted |
| [ADR-0030](#adr-0030-the-item-model-enforces-its-rules-and-wire-formats-stay-at-the-edges) | The item model enforces its rules, and wire formats stay at the edges | Accepted |
| [ADR-0031](#adr-0031-packages-are-told-what-they-need-by-the-composition-root) | Packages are told what they need, by the composition root | Accepted |
| [ADR-0032](#adr-0032-every-request-can-be-checked-against-a-validation-service-failing-closed) | Every request can be checked against a validation service, failing closed | Accepted |
| [ADR-0033](#adr-0033-errors-carry-a-stable-code-from-one-list) | Errors carry a stable code, from one list | Accepted |
| [ADR-0034](#adr-0034-w3c-trace-context-is-carried-through-without-a-tracing-sdk) | W3C trace context is carried through, without a tracing SDK | Accepted |
| [ADR-0035](#adr-0035-upstream-gets-are-retried-once-within-a-budget) | Upstream GETs are retried once, within a budget | Accepted |
| [ADR-0036](#adr-0036-the-api-is-versioned-by-a-header-not-by-path) | The API is versioned by a header, not by path | Accepted |
| [ADR-0037](#adr-0037-the-list-is-paged-by-cursor-50-a-page-by-default) | The list is paged by cursor, 50 a page by default | Accepted |
| [ADR-0038](#adr-0038-the-rate-limit-remembers-a-bounded-number-of-clients-and-an-ipv6-client-is-its-64) | The rate limit remembers a bounded number of clients, and an IPv6 client is its /64 | Accepted |
| [ADR-0039](#adr-0039-items-from-the-upstream-are-held-to-the-models-rules) | Items from the upstream are held to the model's rules | Accepted |
| [ADR-0040](#adr-0040-items-have-etags-and-writes-can-be-conditional) | Items have ETags, and writes can be conditional | Accepted |

## ADR-0001: Record architecture decisions

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** The service grew quickly, through many decisions made in pull requests and
conversations. The code shows what was chosen; nothing showed why, or what was turned down.

**Decision.** Keep architecture decision records, in this one file, in the format above.

**Consequences.** A decision can be revisited knowing its reasons. A record is never edited after
it's accepted, only superseded, so the log is also a history. One file keeps the records inside
the docs site and its link checks.

## ADR-0002: The service uses the standard library only

- **Status:** Accepted
- **Date:** 2026-10-03 (029a3cd)

**Context.** Every dependency is code to audit, update and trust. Go's standard library covers
HTTP serving and clients, JSON, structured logging (`log/slog`), runtime metrics and profiling.

**Decision.** The service's module has no third-party dependencies. Where a tool would normally
bring one, the code does the job itself: flags over plain HTTP instead of an OpenFeature SDK
(ADR-0005), the API spec in JSON instead of YAML (ADR-0008), metrics in Prometheus's text format
written by hand (ADR-0022). Tooling that needs a dependency gets a module of its own: the
integration tests (Godog) and the performance report (benchstat's statistics).

**Consequences.** Nothing to update but Go. Some code exists that a library would otherwise
provide, kept small and tested. `docs/architecture_test.go` fails if the service's `go.mod` gains
a requirement.

## ADR-0003: Items live in an upstream service, behind a store interface

- **Status:** Accepted
- **Date:** 2026-10-03 (8012ea7)

**Context.** The service fronts data owned by another system. Its handlers shouldn't depend on
how that system is reached, and tests shouldn't need it running.

**Decision.** The handlers depend on `item.Store`. The production store, `httpstore`, calls the
upstream's `/items` routes over HTTP. Tests use an in-memory store, and every store must pass the
same contract (`itemtest.StoreContract`).

**Consequences.** The upstream can change, or be replaced, by changing `UPSTREAM_URL` or adding a
store. The service owns no data and no consistency. Upstream failures are the upstream's: 502 or
504, never 500 (REL-1).

## ADR-0004: WireMock is the local upstream

- **Status:** Accepted
- **Date:** 2026-10-03 (a81d892)

**Context.** Local development and the integration tests need an upstream. A hand-written mock
service was the first stand-in; it needed code for every behaviour, faults included.

**Decision.** Use WireMock, with stubs in `wiremock/mappings` and opt-in fault stubs in
`wiremock/faults`, switched on through its admin API (`make fault`).

**Consequences.** Faults (timeouts, resets, malformed bodies) are a JSON file, not code. WireMock
keeps no state: a create returns `new-item` and nothing is stored, which the tests and guides
allow for. It's a JVM, so its warm-up and garbage collection show in load tests' tail latency.

## ADR-0005: Feature flags come from flagd over OFREP, with embedded defaults

- **Status:** Accepted
- **Date:** 2026-10-03 (705e1f0, 8abd719)

**Context.** The service needs feature flags. OpenFeature is the standard; its Go SDK and
flagd's gRPC provider would be the first dependencies (ADR-0002). Flags also mustn't become a
reason for requests to fail.

**Decision.** Run flagd as a sidecar and evaluate flags over OFREP, its plain HTTP/JSON protocol.
`flagd/flags.json` is both what flagd serves and, compiled into the binary, the fallback when
flagd can't answer.

**Consequences.** No SDK, and one file defines each flag. A flagd outage costs flag freshness, not
requests (REL-2), logged once per outage. The service refuses to start if a flag it declares
isn't defined. Only boolean flags are supported so far.

## ADR-0006: Logs are structured and held to a contract

- **Status:** Accepted
- **Date:** 2026-10-03 (8ab7b3f, be75964, d68b8aa)

**Context.** Logs are only useful to query and alert on if their messages, levels and fields are
stable. Free-form log lines drift with every change.

**Decision.** Log with `log/slog`, JSON by default, one line per record. Every field name is a
constant in `internal/logx/keys.go`; every message is in `logtest.Contract` with its levels and
required fields, and in the tables of `docs/logging.md`. Every request gets a correlation ID,
logged as `request_id` and returned in `Client-Correlation-Id`.

**Consequences.** A test fails if the code, the contract and the docs disagree, or if a running
service writes a line the contract doesn't allow (OBS-1). Adding a log line takes three edits.
Secrets in URLs are redacted, and a test plants secrets to prove none is logged.

## ADR-0007: Every outbound call goes through one JSON call

- **Status:** Accepted
- **Date:** 2026-10-04 (fa76e63)

**Context.** The store and the flags client each encoded, sent, capped, decoded and classified
their failures, slightly differently.

**Decision.** `internal/jsoncall` makes every outbound JSON call: it encodes, sends, caps and
drains the response, checks the status, decodes, redacts URLs in errors, and classifies failures.
Each client supplies only what differs.

**Consequences.** A rule for all outbound calls lives in one place: the response cap, error
redaction, and later the refusal to follow redirects (ADR-0019).

## ADR-0008: The OpenAPI spec is the contract, checked against the handlers

- **Status:** Accepted
- **Date:** 2026-10-04 (2e0c9c8)

**Context.** An API spec that's written by hand drifts from the code; one generated from code
needs annotations and tooling.

**Decision.** `docs/openapi.json` is written by hand, in JSON because the standard library has no
YAML parser. Tests run the real handlers through every documented response of every operation and
check status, body schema, headers and limits against the spec, and that the code's routes and
the spec's are the same.

**Consequences.** Changing the API means changing the spec in the same pull request, or a test
fails (API-1). The tests validate with a small JSON Schema subset written for them, so a schema
feature they don't understand fails loudly rather than passing.

## ADR-0009: Integration tests run Gherkin against the composed stack

- **Status:** Accepted
- **Date:** 2026-10-04 (ef95db8)

**Context.** Unit tests run the handlers in-process. Something must also check the real
container, WireMock and flagd together, without tests that manage Docker themselves.

**Decision.** Godog (Gherkin) scenarios in `integration/`, a module of its own, talk HTTP to a
stack started separately (`make integration-up`, as CI does). Tests never call Docker.

**Consequences.** Scenarios read as behaviour, and the stack can be inspected while they run.
Godog is a dependency of the integration module only, never of the service (ADR-0002). Scenarios
assume WireMock's stubs, and that flagd reloads its file, which `integration/flags_test.go`
handles with care.

## ADR-0010: Configuration is environment variables, documented and checked

- **Status:** Accepted
- **Date:** 2026-10-04 (8c85a5f, 41e1d94)

**Context.** The service runs in containers and on platforms that configure through the
environment. Settings described in several places drift apart.

**Decision.** Every setting is an environment variable with its default in code, read through
`internal/envconfig`, which collects every invalid value before failing. `.env.example` lists every
variable with its default, commented out, and the README has a table of them; a test holds the
code, the file and the table to each other.

**Consequences.** No config files. A typo stops startup with every problem at once (CFG-1). Adding
a setting takes three edits, or a test fails (CFG-2).

## ADR-0011: Tooling runs in Docker or through go run

- **Status:** Accepted
- **Date:** 2026-10-04 (a3eba67)

**Context.** Rendering the docs and the spec would normally need pandoc and Node.js (Redocly), and
load testing k6: each a tool for every contributor to install and keep at the right version.

**Decision.** Run them from pinned Docker images (pandoc, Redocly, k6), and Go tools through
`go run` at a pinned version (benchstat, pkgsite, govulncheck). No Node.js anywhere.

**Consequences.** Go and Docker are all a contributor installs (DEP-4), and everyone runs the same
versions. The first run of a target pulls its image.

## ADR-0012: Request headers are screened before the handlers

- **Status:** Accepted
- **Date:** 2026-10-04 (9d94fb6)

**Context.** Headers are untrusted input that every handler would otherwise have to defend
against: oversized headers, duplicated single-use headers, bodies that aren't JSON, and proxy or
method-override headers that could spoof a client's address or method.

**Decision.** `internal/headerguard` screens every request first: limits on header count and size
(431), single-use headers sent twice (400), bodies not sent as JSON (415), and proxy and override
headers removed. It can report instead of enforce, for rolling out.

**Consequences.** Handlers can trust what reaches them (SEC-4). Requiring JSON bodies also blocks
cross-site form posts. Nothing in the service may trust `X-Forwarded-For`, which matters for rate
limiting behind a proxy (ADR-0018). The limits are configurable; which headers are single-use or
removed is not.

## ADR-0013: Pull requests are compared with their base on one runner

- **Status:** Accepted
- **Date:** 2026-10-04 (2236ebf, d94897d)

**Context.** Absolute timings from shared CI runners vary by 10% or more between runners, so a
number from one run means little. A gate on noisy numbers either blocks good changes or is
ignored. Of the load tools considered (a hand-written Go generator, Vegeta as a library, k6), k6
was chosen, run from its image (ADR-0011).

**Decision.** Run each pull request's benchmarks and a short k6 profile against both the pull
request and its base, interleaved on the same runner, and report the comparison in one comment
edited on every push. Fail only on what is exact or plainly broken: more allocations per
operation (unless the pull request has the `perf-ok` label) or crossed load thresholds. Report
timing, CPU and memory changes as warnings. Write the comment from a job that runs no pull
request code (ADR-0020).

**Consequences.** Regressions in allocations can't land unnoticed (PERF-2). Timing regressions
are flagged but need confirming locally (`make perf-compare`). The comparison adds about four
minutes to a pull request, in parallel with CI.

## ADR-0014: Resource monitoring adds containers, not dependencies

- **Status:** Accepted
- **Date:** 2026-10-05 (04e0aa5)

**Context.** Load tests need CPU and memory figures beside their latencies. Exporters and client
libraries would add dependencies to the service.

**Decision.** Three layers: the service serves the Go runtime's own metrics with the standard
library (ADR-0022); every load run samples `docker stats` and the service's metrics before and
after; and `make monitor` starts cAdvisor, Prometheus and Grafana as an opt-in compose profile,
with a dashboard kept in the repository.

**Consequences.** The pull request report includes CPU per request and peak memory. The dashboard
is local-only and on loopback. cAdvisor needs privileged access to the Docker engine, so it runs
only when asked for.

## ADR-0015: The domain-driven restructure is deferred

- **Status:** Accepted
- **Date:** 2026-10-05 (pull request #1, closed)

**Context.** A restructure along domain-driven lines was built: a pure domain package, a name value
object with its invariants, HTTP handlers and DTOs in their own package, and the store's wire
types as an anti-corruption layer. Items are a placeholder with CRUD rules only.

**Decision.** Don't adopt it now. Keep `internal/item` as model, store interface and handlers
together, until a real domain with real rules replaces items.

**Consequences.** Less structure than a growing domain would need. When one arrives, pull request
#1 shows a worked restructure to start from; its branch was deleted, and GitHub can restore it.

## ADR-0016: Clients authenticate with hashed API keys

- **Status:** Accepted
- **Date:** 2026-10-05 (f765a76)

**Context.** Anyone who could reach the service could change its data. The options were API keys
checked in the service, JWT or OIDC (a dependency, or a careful implementation), or leaving it to
a gateway in front.

**Decision.** API keys, sent as `Authorization: Bearer`. `API_KEYS` lists each key's name, scope
(read, or read and write) and SHA-256 hash; a request's key is hashed and compared with every
configured hash in constant time. Keys are 256-bit random values from `make api-key`, so a fast
hash is safe. Every route but the probes needs a key; the service refuses to start with none,
unless `API_AUTH=off`, which logs a warning.

**Consequences.** No dependency, and a leaked config holds no usable key (SEC-1, SEC-2). The local
stack ships two documented development keys, which work only on loopback (ADR-0020). Keys are
read once at startup, so rotating one needs a restart. A gateway can still be added in front.

## ADR-0017: Item IDs are checked against an allowlist

- **Status:** Accepted
- **Date:** 2026-10-05 (f3a2197)

**Context.** IDs were path-escaped before going to the upstream, but `%2E%2E` reached it as
`/items/..`, which an upstream or proxy that normalizes paths, or decodes `%2F`, could route
elsewhere. Answering 400 or 404 for such an ID was considered.

**Decision.** An item ID is 1 to 128 letters, digits, `-` and `_` (`item.ValidID`). The handlers
answer any other with 404, as for an item that doesn't exist, without asking the store; the
store refuses one too.

**Consequences.** No path traversal into the upstream (API-4). The contract didn't change: an
impossible ID is just an unknown one. An upstream that issues other IDs would need the rule
widened, in the spec and the code together.

## ADR-0018: The service sheds load itself

- **Status:** Accepted
- **Date:** 2026-10-05 (810139f)

**Context.** Every request fanned out to the upstream, so a flood passed straight through. Limits
in a gateway would protect the service only where there is one.

**Decision.** `internal/limit` caps requests in flight (503) before authentication, and limits each
client with a token bucket (429) after it. A client is its API key, or its address without one.
Both refuse at once with `Retry-After` rather than queue. The probes are exempt.

**Consequences.** The upstream sees at most the service's capacity (REL-3). Limits are per
instance. Behind a proxy, keyless clients share the proxy's address, since proxy headers are never
trusted (ADR-0012). The local stack turns the per-client limit off so load tests measure the
service.

## ADR-0019: Outbound calls never follow redirects

- **Status:** Accepted
- **Date:** 2026-10-05 (463a27f)

**Context.** Go's HTTP client follows redirects by default. A compromised or misconfigured
upstream could send the service to any address it can reach, internal ones included, with the
request body on a 307 or 308.

**Decision.** `jsoncall` never follows a redirect, whatever client it's given: a 3xx is an
unexpected status, so a 502. It keeps one no-redirect copy of each client, so this costs nothing
per call.

**Consequences.** No server-side request forgery through the upstream (SEC-5). An upstream that
moves must be reconfigured, not followed.

## ADR-0020: CI pins, updates, scans and isolates

- **Status:** Accepted
- **Date:** 2026-10-05 (546210d, b9a93e9)

**Context.** CI runs third-party actions and images, and the performance workflow writes a comment
to the pull request: a token with write access in a job that runs the pull request's code.

**Decision.** Pin every action to a commit SHA and every image to a digest; let Dependabot propose
updates weekly; run govulncheck on every push. Run pull request code only in read-only jobs, and
post the comment from a job that never checks it out. Publish the local stack on loopback only.

**Consequences.** A moved tag can't change what runs (SEC-8). Updates arrive as reviewable pull
requests, each with its own performance report. The pandoc and Redocly digests in the Makefile are
outside what Dependabot reads and are updated by hand.

## ADR-0021: Readiness means the upstream can be reached

- **Status:** Accepted
- **Date:** 2026-10-05 (1330a94)

**Context.** A load balancer needs to know which instances to send traffic to. If readiness
followed the upstream's health, an upstream problem would take every instance out of service at
once, turning partial failure into total outage.

**Decision.** `/readyz` is ready while the service can reach the upstream (any HTTP answer counts,
an error included) and isn't shutting down. `/healthz` stays the liveness check. Results are
cached for a second. `SHUTDOWN_DELAY` keeps serving after SIGTERM, reporting not ready.

**Consequences.** Instance-local problems (network, DNS, configuration) take an instance out of
service; upstream problems surface as 502s from every instance instead (REL-4). Deploys can drain
cleanly (REL-5).

## ADR-0022: Metrics are Prometheus text, served privately

- **Status:** Accepted
- **Date:** 2026-10-05 (04e0aa5, 8b47875)

**Context.** Operators need request rates, errors and latencies, and the runtime's state. The
Prometheus client library would be a dependency (ADR-0002). Profiles and metrics also reveal
internals.

**Decision.** Write Prometheus's text format with the standard library: every scalar from
`runtime/metrics`, and per-route request counts and latency histograms (`internal/httpmetrics`)
with bounded labels and no allocation per request. Serve them, with pprof, on `PPROF_ADDR` only:
off by default, never the API's port.

**Consequences.** Any Prometheus can scrape the service (OBS-3), and `make monitor` does. Names
follow the Prometheus Go client's, so dashboards carry over. Exposure is the deployment's to keep
private; the service warns when it listens beyond loopback (SEC-6).

## ADR-0023: A request's log lines share state

- **Status:** Accepted
- **Date:** 2026-10-05 (8b47875, c3cd08b)

**Context.** Middleware hands inner handlers copies of the request. What an inner handler learned
(the route the router matched, the API key that authenticated) never reached the outer logging
middleware: the `route` field of every "response sent" line was empty once authentication was
added.

**Decision.** The correlation ID's context value is a small per-request state, shared by every
copy of the request, which inner handlers add to: `httpx.RecordRoute` records the route, and
authentication adds the key's name to every later log line. It costs the one allocation storing
the ID already did.

**Consequences.** Every line of a request carries its ID, route and key (OBS-2), with no change to
handler signatures. Anything else learned deep in a request can be logged the same way.

## ADR-0024: Main's performance history lives on a data branch

- **Status:** Accepted
- **Date:** 2026-10-05 (9f7c99b)

**Context.** Pull request comparisons (ADR-0013) catch a change worse than its base, not a slow
drift across merges. Dashboards and benchmark services need hosting; GitHub Pages needs a paid
plan for a private repository.

**Decision.** After every merge, measure `main` alone and save one JSON record per commit on the
`perf-data` branch. `make perf-trends` charts the records as a standalone HTML page, drawn with
inline SVG by the report tool.

**Consequences.** The history lives in git, with no service to run (PERF-3). The recording job is
the only one that writes to the repository, only to that branch, and runs only merged code.
Timings carry the runners' noise; allocations, being exact, chart cleanly.

## ADR-0025: CI keeps Go's build cache, not Docker's layers

- **Status:** Accepted
- **Date:** 2026-10-05 (80c5283)

**Context.** CI spent most of the image build compiling from an empty Go build cache. Docker's
layer cache (through `docker/bake-action`) wouldn't help: nearly every pull request changes Go
code, which invalidates the compile layer.

**Decision.** Keep Go's own build cache between runs with `actions/cache`, fed into the image
build as an optional build context and exported after it (`scripts/build-api-image.sh`).

**Consequences.** Only changed packages recompile: the integration job's image build went from 40s
to 29s. Local builds are unchanged. The cache is keyed on the Dockerfile and `go.mod`, so a new Go
version starts afresh rather than growing the old cache.

## ADR-0026: Static analysis is gosec and staticcheck, not CodeQL

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** Tests, fuzzing, `go vet` and govulncheck catch a lot, but not every security mistake or
suspicious construct. CodeQL, GitHub's analysis, was the first choice; on a private repository it
needs GitHub Advanced Security, which this repository doesn't have, and its licence allows it on
private code only with that.

**Decision.** Run two open-source analysers for Go through `go run` at pinned versions (ADR-0011):
staticcheck, for bugs and suspicious code, and gosec, for security mistakes, on all three modules,
as `make lint` and a CI job. A finding is fixed, or accepted on its own line with the rule and the
reason (`// #nosec G304 -- why`). The report tool, whose job is reading the files named on its
command line, skips gosec's file-inclusion rule.

**Consequences.** No licence or setting needed (SEC-9). Their first run found a fragile test
condition, an unchecked hex decode in `apikey.ParseKeys`, and five unhandled `Close` errors, all
fixed, plus one false positive accepted. Results live in the CI log, not GitHub's Security tab. If
the repository becomes public or gains Advanced Security, CodeQL can be added beside them.

## ADR-0027: A release is a tag on main, published by CI

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** There were no versions: the changelog was one long Unreleased section, and the image
was built only locally and in CI, never published. A deployment needs a versioned image, and
needs to know what's in it.

**Decision.** A release is a version tag on a commit on `main`. `make release-prepare` cuts the
changelog section in a pull request; `make release-tag` tags `main` once it's merged. The tag
starts a workflow that publishes the image to the GitHub Container Registry for amd64 and arm64,
cross-compiled with no emulation, with an SBOM and build provenance made by BuildKit, and a
GitHub release with the changelog section as its notes. Anything else, a branch or a tag off
`main`, publishes nothing. GitHub's artifact attestations aren't available to private repositories
on this plan, and signing with cosign would need a third-party tool, so images carry provenance
but no signature.

**Consequences.** Versions are reviewed (DEL-1) and their images are reproducible from a commit,
with their contents and build recorded (DEL-2). A dry run rehearses the workflow without
publishing. The SBOM scanner's image is pinned by digest in the workflow, outside what Dependabot
reads, like the pandoc and Redocly images (ADR-0020).

## ADR-0028: API keys can be rotated from a file, without a restart

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** ADR-0016 read `API_KEYS` once, so rotating a key, or revoking a leaked one, meant a
restart. It also gave every key the same rate limit, though a batch job and an interactive client
need different ones. Watching a file for changes, with fsnotify, would be a dependency
(ADR-0002).

**Decision.** `API_KEYS_FILE` names a file of the same entries, one per line, which the service
checks every `API_KEYS_RELOAD` (10s) by polling and swaps in atomically once a change has read the
same on two checks in a row, so a file caught half-written is never used. A file
that can't be read or parsed, or holds no keys, changes nothing and is logged once, so a bad edit
can't lock every client out. An entry may add a fourth field, the key's own rate limit, which
replaces `RATE_LIMIT` for it and applies even when that's off. This amends ADR-0016's consequence
that rotation needs a restart; the rest of it stands.

**Consequences.** Keys rotate with no downtime: add the new key, move clients to it, remove the
old one (SEC-10). Polling suits a mounted secret, which is swapped rather than edited. A change
takes up to twice `API_KEYS_RELOAD` to apply. `API_KEYS` and `API_KEYS_FILE` can't both be set.

## ADR-0029: Flag values are cached, and a flagd outage is retried once per TTL

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** ADR-0005 evaluated every flag by asking flagd, on every request that reads it: a
network round trip on the request path, and load on flagd that grows with the API's traffic. Worse,
a flagd that stops answering without refusing connections made every such request wait out
`FLAGS_TIMEOUT` (500ms) before taking the default, so an outage that costs no requests still costs
every one of them half a second. The OpenFeature SDK's flagd provider avoids both by streaming
flag changes over gRPC, which would be a dependency (ADR-0002).

**Decision.** The OFREP client keeps each value flagd serves for `FLAGS_CACHE_TTL` (1s). Only
values flagd served are kept: a failed evaluation is asked again next time. While flagd isn't
answering, it is asked at most once per TTL, by whichever evaluation comes first, and every
evaluation in between takes its default at once, counted in the outage's `fallbacks`. `0s` turns
both off and asks flagd every time, as before. This amends ADR-0005's consequence that a flagd
outage costs only flag freshness; it now costs a request nothing but the default either.

**Consequences.** A flag change takes up to the TTL to reach the API, and instances may disagree
for that long, which a second makes harmless for a feature toggle but would matter for a kill
switch that must act at once (set the TTL lower, or to `0s`). flagd sees about one request per flag
per TTL per instance, whatever the traffic. An outage costs one `FLAGS_TIMEOUT` per TTL instead of
one per request. The integration tests run with the cache off, so a flag step's change applies to
the next request.

## ADR-0030: The item model enforces its rules, and wire formats stay at the edges

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** ADR-0015 deferred a domain-driven restructure until a real domain replaces items.
Reviewing the layout against domain-driven design found two flaws that don't wait for one. The
model was anemic: an item's rules (a name required and at most 200 characters, a description at
most 2000) were checked only by the HTTP handlers, so code that made an item any other way could
skip them. And the model carried JSON tags, so the API's wire format and the upstream's were both
the domain type's: a change to either reached into the model.

**Decision.** Take the part of the restructure that fixes those, and leave the layering. In
`internal/item`, an `ID`, a `Name` and a `Description` are value objects that can only be made
valid (`ParseID`, `NewName`, `NewDescription`), and a `Draft`, what a client asks to create or
replace, only from a valid name and description (`NewDraft`). The `Store` takes IDs and drafts, not
loose items. `Item` has no JSON shape: the handlers' `Request` and `Response` are the API's, and
`httpstore`'s own types are the upstream's, each translated at its edge. A store rebuilds the
items it holds with `Restore`, which doesn't re-check the rules: they govern what clients send,
and refusing an item the upstream sends that breaks them is a separate decision, not taken here.
This amends ADR-0015: the model and its edges change; the single package, without domain,
application and infrastructure layers, stands.

**Consequences.** The rules hold however an item is made, and an invalid ID can't reach a store or
an upstream URL by construction, not only by the handlers' check. The API's and the upstream's
JSON can change independently of the model and of each other. Every request translates between
shapes: listing items costs two more allocations a request (a slice each side). Tests make items
through the constructors (`itemtest.MustDraft`, `itemtest.MustID`, `item.Restore`). When a real
domain arrives, its rules have a place to live, and ADR-0015's layering is the next step if it
needs one.

## ADR-0031: Packages are told what they need, by the composition root

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** A review of coupling found three packages reaching into others for something they
could be told. `httpstore`, the upstream adapter, chose the API's 502 and 504 itself, by
implementing `httpx.StatusError`: the adapter knew how the API answers its clients. The rate limiter
read `apikey`'s request context to learn who a client was and its limit, so changing
authentication meant changing the limiter. And `item` declared its flag in `featureflag`'s
package-level registry, filled as packages initialised, which `cmd/api` then read: global state
shared by every test, and a dependency for the sake of one string.

**Decision.** Each package states what it knows, and `cmd/api`, the composition root, connects
them. `httpstore`'s errors say what went wrong, matching `item.ErrStoreTimeout` or
`item.ErrStoreUnavailable`, and the item handlers decide those are 504 and 502. The limiter takes an
`Identify` function (who is this client, and what's their own limit?) and knows nothing of how it
answers; `cmd/api` passes one built from the API keys. `item` lists the flags its handlers read
(`FlagKeys`), and `cmd/api` checks them against the definitions; the registry is gone. The package
table in `docs/architecture.md` now fails on an import it allows that a package doesn't make, so it
reads as the dependencies there are.

**Consequences.** `httpstore` no longer imports `httpx`, `limit` no longer imports `apikey`, and
`item` no longer imports `featureflag`. Another interface over the store, another way to
authenticate, or another package with flags plugs in at `cmd/api` without touching them. The cost
is explicitness: `cmd/api` has a little more wiring, and a flag must be listed as well as named,
which a test in `internal/item` checks.

## ADR-0032: Every request can be checked against a validation service, failing closed

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** An API may not be able to serve a request until another service approves it: a fraud
or entitlement check, a policy engine, token introspection. Such a call sits on every request's
path, so its latency is every request's, and its outages are every request's unless the API is
careful.

**Decision.** `internal/validator` checks every request but the probes against the service at
`VALIDATOR_URL`, as a middleware after authentication and the per-client rate limit: it knows
who's calling, and a client's flood never reaches the service. The call goes through `jsoncall`,
like every outbound call (ADR-0007, ADR-0019), with its own connection pool and a short timeout
(`VALIDATOR_TIMEOUT`, 300ms). A refusal is a 403 whose reason is logged, never sent. When the
service can't answer, the API fails closed by default (503 with `Retry-After`), or open with
`VALIDATOR_FAILURE_MODE=open`; either way it asks again at most once a second, answering the
requests in between at once, and logs an outage once each way, as for flagd (ADR-0029). Failing
closed, `/readyz` also requires the service to be reachable (ADR-0021). The request and response
shapes are a placeholder for the real service's. Whether answers are acted on is the
`request-validation-enforce` feature flag, off by default: shadow mode asks the service and logs
what it would have done, refusing nothing, so the check can be deployed dark and switched on, or
back off, at runtime; a flagd outage falls back to shadow, never to refusing. The API measures the
calls (`validator_call_duration_seconds`, `validator_calls_total{outcome}`), and the performance
report and `main`'s trends track them.

**Consequences.** Every request costs a call to the validation service, shadow mode included, by
design: the dark phase measures what enforcing will cost. A cache can come when the real service's
rules say what can be cached. A validation outage, failing closed, is an
outage of the API, by design: the check isn't skipped quietly. Without `VALIDATOR_URL` nothing is
checked, logged as a warning at startup. The local stack runs it against a WireMock stub, so the
load tests include its cost.

## ADR-0033: Errors carry a stable code, from one list

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** Every error was `{"error": "..."}`, a message for people. A client acting on errors
had only the status, and some statuses mean several things: a 403 for a read-only key or for a
refusal by the validation service (ADR-0032), a 503 for a busy service or an unavailable
validation service. Telling them apart meant matching message text, which then couldn't be
reworded.

**Decision.** Every error body also carries `code`: `{"error": "...", "code": "rate_limited"}`.
The codes are one list, `httpx.Code` in `internal/httpx/codes.go`, and `httpx.WriteError` takes
one, so no error can be written without it. The OpenAPI spec's Error schema lists them as an enum,
which tests hold to the list both ways, and every scenario's response is checked against it; the
README's table of statuses lists them too, also tested. "response sent" logs the code as
`response_code`. The change is additive: `error` is unchanged, so no client breaks. A format such
as RFC 9457's `application/problem+json` was not adopted: it changes the body's shape, which only
clients asking for it would justify.

**Consequences.** Clients switch on `code`, and messages can be reworded freely. A new kind of
error needs a code added to the list, the spec and the README, which the tests enforce. Logs and
dashboards can group errors by meaning.

## ADR-0034: W3C trace context is carried through, without a tracing SDK

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** OpenTelemetry is planned, but its Go SDK would be the service's first dependency
(ADR-0002), and until it's added, a request that crosses this service loses its trace: the calls
the service makes to the upstream, the validation service and flagd start no trace, or a new one.
Carrying the trace context through is the part of tracing that needs no SDK.

**Decision.** `internal/tracectx` implements W3C Trace Context's `traceparent`: the middleware takes
a request's from a valid header, or starts a trace (unsampled, since nothing records it), and keeps
it in the request's log state (`logx.SetTrace`), so every log line of the request carries
`trace_id`. Every outbound call, through `jsoncall`, sends `traceparent` with the request's trace
and flags and a span ID of its own. `tracestate` is not carried, and no spans are recorded.

**Consequences.** A caller's trace ID finds this service's log lines for its request, and the
upstream and the validation service see the same trace. When an OpenTelemetry SDK is added, its
spans join traces that already flow, and it replaces the generated span IDs with real ones. Each
request costs an allocation for its trace ID's text, and each outbound call one for its header.

## ADR-0035: Upstream GETs are retried once, within a budget

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** A call to the upstream can fail for a moment: a connection dropped by a load
balancer, an instance restarting. Each such blip became a 502 for the client, though the same
request a moment later would have worked. Retrying blindly is worse, though: it repeats writes,
doubles the wait after a timeout, and when the upstream is really down, multiplies its load just
when it can least take it.

**Decision.** `httpstore` tries a GET again, up to `UPSTREAM_RETRIES` times (1 by default, at most
3), when the upstream gave no usable answer fast: a refused or reset connection, or a 5xx. Never a
write, never a timeout, never a 4xx (an answer, which a retry would only repeat), and never once the
client has gone. Retries pause briefly, jittered (25 to 50ms), and share a token-bucket budget:
each request earns a tenth of a retry, up to ten saved, so retries add at most about 10% to the
upstream's traffic however many requests fail. Each retry is logged, `upstream request retried`.

**Consequences.** A blip costs a client a few tens of milliseconds instead of a 502. A down upstream
sees about 10% more traffic, not double, and clients get their 502s as before once the budget is
spent. Writes still fail on the first error, and so stay the caller's to retry, knowing whether the
write is safe to repeat.

## ADR-0036: The API is versioned by a header, not by path

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** The API is at 0.x, with no external clients yet. Once it has them, a breaking change
needs a way to serve the old behaviour beside the new; adding that way later is itself a breaking
change, so it's cheapest to decide now. The usual choices are a path prefix (`/v1/items`) or a
header.

**Decision.** A header, `API-Version`. A request may send it; without it, it gets the current
version. Every response carries the version that answered, set by the standard middleware so the
responses refused early carry it too. A version the API doesn't serve is a 400 with
`unsupported_api_version`, checked after header screening and before authentication; the probes
answer any. `API-Version` may appear only once. The versions are one list,
`httpx.APIVersions`, today `1`. The request isn't marked with its version, which would cost an
allocation a request while there's one: a handler that comes to differ between versions reads the
header.

**Consequences.** URLs never change with a version, so links, routes and load-balancer rules stay
as they are. A version is less visible than in a path: it's in a header, not in logs of URLs or in
links. A breaking change adds a version to the list and serves both; clients that sent the header
keep what they were written against, and those that didn't move to the new one, so clients that
care should send it.

## ADR-0037: The list is paged by cursor, 50 a page by default

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** `GET /items` returned every item in one response, as much as the upstream sent (up to
4 MiB). That grows with the data, and can't be bounded later without breaking the clients that
rely on getting everything, so it's cheapest to decide before there are any.

**Decision.** The list comes a page at a time, by cursor: `?limit` (1 to 200, 50 without it) and
`?cursor`, with `next_cursor` in the response until the last page. A cursor marks the place after a
page's last item, not an offset, so pages stay right while items are added or deleted: nothing is
skipped or listed twice. Cursors are opaque, made by the store: the in-memory one encodes the last
item's creation time and ID; `httpstore` passes the API's page through to the upstream, which pages
the same way, and its cursors back. A client's cursor is checked for form (1 to 256 URL-safe
characters) before it reaches an upstream URL; a `limit` out of range, or a cursor the store
didn't give out (the upstream's 400), is a 400, `invalid_parameter`. The `total` the
`items-list-total` flag adds counts the page. A list's query string, the cursor included, is never
logged.

**Consequences.** Every response is bounded, by default. A client that ignored paging now gets the
first 50 items, not all of them: a behaviour change, made while there are no clients to break.
Paging means more requests for a client that wants everything, and the upstream must page too.

## ADR-0038: The rate limit remembers a bounded number of clients, and an IPv6 client is its /64

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** The per-client rate limit (ADR-0018) keeps a token bucket per client, and forgets the
idle ones once a minute. A client without a key is its IP address, and with IPv6, one subscriber
typically has a /64 to move around in for free: by changing address on every request, it could
take a fresh limit each time, escaping the limit, and fill memory with buckets between sweeps.

**Decision.** An IPv6 client is its /64; an IPv4 client, its address, as before. The limiter
remembers at most `MaxClients` (100,000, about 10 MB) at once: when it's full, it forgets the idle
clients at once rather than waiting for the minute's sweep, and if none are idle, new clients share
one limit, the overflow, until there's room.

**Consequences.** Memory for the limit is bounded, whatever the traffic. Clients sharing a /64
share a limit, which a network that gives many users one /64 would feel. In a flood from more
than 100,000 active addresses, newcomers are limited together, which throttles the flood rather
than each of its addresses; clients the limiter already knows keep their own limits.

## ADR-0039: Items from the upstream are held to the model's rules

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** The model holds what clients send to its rules (ADR-0030), but items the upstream sent
back were restored as they came, unchecked: a decision deferred then. An upstream with a bug, or a
compromised one, could send an item the API couldn't have made: an ID such as `../admin`, which
goes into `Location` headers and later URLs, a name over the limit the API's own contract
promises, or no timestamps. The API would pass it on, breaking its contract with its clients.

**Decision.** `httpstore` builds every item the upstream sends with `item.Parse`, which holds it to
the rules a client's input meets: a valid ID, a name and description within their limits (the name
trimmed), and both timestamps. An item that breaks one makes the call an unusable answer, like a
body that won't decode: logged with which rule (never the value, which came from outside), a 502
to the client, and not retried, since the same request would get the same item. One bad item fails
its whole page. `item.Restore` stays for stores whose items can only have come through the model.

**Consequences.** The API's responses keep its contract whatever the upstream does. An upstream
that sends bad data is visible at once, as 502s and their log lines, rather than as bad data
passed on. A single bad item makes its page unreadable until the upstream is fixed: strict, but
the alternative, skipping it, would hide data silently.

## ADR-0040: Items have ETags, and writes can be conditional

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** A `PUT` replaced an item whatever it had become since the client read it, so two
clients editing the same item would each overwrite the other's change without knowing: a lost
update. HTTP's conditional requests are the standard answer.

**Decision.** Every item response (`GET`, `POST` and `PUT` on an item) carries an `ETag`: an
FNV-1a hash of the item's name, description and last update time, so it changes with any change,
even two in one tick of the clock. A `PUT` or `DELETE` with `If-Match` goes ahead only if the item
still has a listed tag (compared strongly, so a weak tag never matches), or for `*`, exists at
all; otherwise it's a 412 with `precondition_failed`, and nothing changes. A `GET` with a matching
`If-None-Match` (compared weakly) is a 304 with no body. Without the headers, requests behave as
before. The check is in the handlers: they read the item, compare, then write.

**Consequences.** Clients that send `If-Match` can't lose each other's updates in the common case,
a client acting on a stale copy. Two writers racing between the check and the write can still both
pass; closing that needs the upstream to take `If-Match` itself, so the comparison and the write
are one step, which the contract can grow when there's a real upstream. A conditional write costs
one more upstream read. The tag is the API's own, not the upstream's.
