# Architecture

How the service is built, what it must do, and how each requirement is verified. Why it's built
this way is in the [architecture decisions](decisions.md), which this document refers to as
ADR-NNNN.

The tables of packages and requirements below are checked by `docs/architecture_test.go`: the
code may import only what the package table allows, and every file a requirement names as its
verification must exist.

- [Context and scope](#context-and-scope)
- [Building blocks](#building-blocks)
- [Runtime behaviour](#runtime-behaviour)
- [Deployment](#deployment)
- [Requirements](#requirements)
- [Constraints and assumptions](#constraints-and-assumptions)
- [Known gaps](#known-gaps)

## Context and scope

The service is an HTTP JSON API over a placeholder resource, `items`, standing in for a real
domain. It owns no data: items live in an upstream service that speaks the same `/items` contract
over HTTP (ADR-0003). Feature flags come from a flagd sidecar, when there is one (ADR-0005).

```
                        Authorization: Bearer <key>
 API clients ──HTTP──▶ [ load balancer / TLS ] ──▶ api :8080 ──HTTP──▶ items upstream
                              │ GET /readyz           │   │
                              ▼                       │   └──OFREP──▶ flagd (optional)
 operators ◀── logs on stdout · /metrics and pprof on PPROF_ADDR (private) · /healthz
```

| Who | What they need from the service |
| --- | ------------------------------- |
| API clients | The `/items` operations in `docs/openapi.json`, authenticated with an API key |
| The items upstream | Well-formed requests to `/items` and `/items/{id}` only, never more than the service's own traffic |
| A load balancer or orchestrator | `/healthz` (restart if not alive) and `/readyz` (route traffic only while ready) |
| Operators | Structured logs with a correlation ID per request, request and runtime metrics, profiles on demand |
| Developers | A local stack (`make up`) and tests that need no running containers |

**Out of scope:** storing data (the upstream owns it), TLS (terminated in front of the service),
user identities (clients are API keys; [known gaps](#known-gaps)), and distributed tracing.

## Building blocks

### The request path

Every request passes through the same chain, built by `newHandler` in `cmd/api/main.go`, from the
outside in:

| # | Stage | Package | Refuses with | Why here |
| - | ----- | ------- | ------------ | -------- |
| 1 | Defensive headers, correlation ID, request logging and metrics, panic recovery | `internal/httpx` | 500 on a panic | Outermost, so every response, refusals included, is logged, counted and carries a correlation ID |
| 2 | Cap on requests in flight | `internal/limit` | 503 | Before authentication, so floods without a valid key are capped too (ADR-0018) |
| 3 | Header screening | `internal/headerguard` | 400, 415, 431 | Before authentication, so a doubled `Authorization` header is refused before either copy is read (ADR-0012) |
| 4 | Authentication | `internal/apikey` | 401, 403 | After screening; the probes are exempt (ADR-0016) |
| 5 | Per-client rate limit | `internal/limit` | 429 | After authentication, so a client is its key (ADR-0018) |
| 6 | Routing | `net/http.ServeMux`, wrapped by `httpx.RecordRoute` | 404, 405 | Innermost, so the matched route reaches the outer stages (ADR-0023) |
| 7 | Handlers | `internal/item` | 400, 404, 422 | Decode, validate (IDs per ADR-0017), call the store |
| 8 | The store | `internal/item/httpstore`, over `internal/jsoncall` | 502, 504 | Upstream failures become 502, or 504 on a timeout, never 500 (ADR-0007, ADR-0019) |

`/healthz` and `/readyz` sit at stage 6 but skip stages 2, 4 and 5: a busy or misconfigured
service must still answer its probes.

### Packages and their dependencies

`go list` shows the imports; this table is what they may be. A change that needs another import
updates this table, and so the architecture, in the same pull request.

| Package | Kind | Role | May import |
| ------- | ---- | ---- | ---------- |
| `cmd/api` | composition root | Reads config, wires everything, runs the server | any runtime package |
| `cmd/apikey` | tool | Generates an API key and its `API_KEYS` entry | `internal/apikey` |
| `internal/item` | feature | The `Item` model, the `Store` interface, the `/items` handlers | `internal/featureflag`, `internal/httpx` |
| `internal/item/httpstore` | adapter | The `Store` on the upstream, over HTTP | `internal/item`, `internal/httpx`, `internal/jsoncall`, `internal/logx` |
| `internal/featureflag` | adapter | OFREP client for flagd, flag declarations, embedded defaults | `flagd`, `internal/jsoncall`, `internal/logx` |
| `internal/headerguard` | protection | Screens request headers | `internal/httpx`, `internal/logx` |
| `internal/apikey` | protection | Authenticates API keys | `internal/httpx`, `internal/logx` |
| `internal/limit` | protection | In-flight cap and per-client rate limit | `internal/apikey`, `internal/httpx` |
| `internal/httpx` | plumbing | Serving, middleware, JSON helpers, readiness | `internal/logx` |
| `internal/jsoncall` | plumbing | One JSON-over-HTTP call, for every outbound client | `internal/logx` |
| `internal/envconfig` | plumbing | Environment variables and the settings every service shares | `internal/logx` |
| `internal/profiling` | plumbing | pprof and runtime metrics on a private listener | `internal/logx` |
| `internal/httpmetrics` | foundation | Request metrics in Prometheus's text format | nothing |
| `internal/logx` | foundation | The logger, field names, per-request log state, redaction | nothing |
| `flagd` | foundation | The flag definitions, embedded as the fallback defaults | nothing |
| `internal/item/itemtest` | test support | In-memory `Store` and the contract every `Store` must pass | `internal/item` |
| `internal/logtest` | test support | Capturing and parsing logs; the log contract | nothing |
| `internal/nettest` | test support | Dead, hanging and failing upstream stubs | nothing |

Only test files may import a test support package. The foundations import nothing from the
module, so everything can use them without a cycle.

## Runtime behaviour

**A request.** The chain above, in order. The correlation ID is assigned at stage 1, stored in
the request's shared log state with, later, the matched route and the API key's name, so every
log line of the request carries them (ADR-0023). Each request writes `request received` and
`response sent`, plus `upstream request sent` and `upstream response received` for each call to
the upstream.

**When the upstream fails.** A call that times out (`UPSTREAM_TIMEOUT`, 5s) is a 504, and any other
failure (refused, reset, an error status, a body that won't decode, a redirect) a 502. The
upstream's message is logged, never returned. `/readyz` stays ready while the upstream answers at
all, an error included (ADR-0021).

**When flagd fails.** Flag evaluation never fails a request: an unreachable, slow
(`FLAGS_TIMEOUT`, 500ms) or confused flagd means the flag's default from `flagd/flags.json`, logged
once per outage (ADR-0005).

**Under load.** At most `MAX_IN_FLIGHT` requests (512) are served at once, and each client may
make `RATE_LIMIT` requests a second (100) in bursts of `RATE_BURST` (200). Beyond that the service
refuses at once, with `Retry-After`, rather than queueing (ADR-0018).

**Shutting down.** On SIGTERM, `/readyz` turns 503, the service keeps serving for
`SHUTDOWN_DELAY` (0 by default) so load balancers can stop sending requests, then stops accepting
connections and gives in-flight requests up to `SHUTDOWN_TIMEOUT` (10s) to finish.

## Deployment

One stateless container: the static binary on `gcr.io/distroless/static-debian12:nonroot`, pinned
by digest, run as a non-root user. It needs no disk and no Linux capabilities (the compose stack
runs it read-only, with `cap_drop: [ALL]` and `no-new-privileges`).

| Port | Serves | Exposure |
| ---- | ------ | -------- |
| `ADDR` (`:8080`) | The API, `/healthz`, `/readyz` | Behind the load balancer |
| `PPROF_ADDR` (unset, so off) | pprof and `/metrics` | Private: never the API's port, and a WARN at startup when other machines can reach it |

Configuration is environment variables only, with defaults in code (ADR-0010). The service refuses
to start on an invalid value, or with authentication on and no keys.

Locally, `compose.yaml` runs the API with WireMock as the upstream and flagd, all published on
`127.0.0.1` only. Opt-in profiles add k6 (`perf`) and cAdvisor, Prometheus and Grafana
(`monitoring`).

## Requirements

Each requirement has an ID for reference from issues and pull requests. "Verified by" names the
test or check that fails when the requirement doesn't hold; a requirement verified only by review
says so.

### Dependencies and build

| ID | Requirement | Why | Verified by |
| -- | ----------- | --- | ----------- |
| DEP-1 | The service depends on the Go standard library only. | No dependency to audit, update or be compromised through (ADR-0002) | `docs/architecture_test.go` |
| DEP-2 | Tooling that needs a dependency lives in a module of its own, which the service never imports. | Keeps DEP-1 while allowing Godog and benchstat's statistics | `docs/architecture_test.go` |
| DEP-3 | Packages import only what the package table allows; test support only from tests. | The layering stays as designed | `docs/architecture_test.go` |
| DEP-4 | Developer tooling needs only Go and Docker, besides the bash and curl every Mac and Linux machine has: no Node.js, nothing else to install. | Least friction for contributors (ADR-0011) | Review: `Makefile` |

### API contract

| ID | Requirement | Why | Verified by |
| -- | ----------- | --- | ----------- |
| API-1 | The service does exactly what `docs/openapi.json` says: routes, status codes, bodies, headers and limits. | Clients can rely on the spec (ADR-0008) | `cmd/api/openapi_test.go`, `cmd/api/openapi_routes_test.go`, `cmd/api/openapi_params_test.go` |
| API-2 | Every error is JSON, `{"error": "..."}`, and never reveals internals: 5xx messages are generic, and decode errors speak JSON, not Go. | Safe to show any caller | `cmd/api/openapi_test.go`, `internal/httpx/fuzz_test.go` |
| API-3 | Every response carries a correlation ID, `X-Content-Type-Options: nosniff` and `Cache-Control: no-store`. | Traceable, and never sniffed or cached | `cmd/api/security_test.go`, `cmd/api/openapi_test.go` |
| API-4 | An item ID is 1 to 128 letters, digits, `-` and `_`; any other is a 404 that never reaches the upstream. | No path traversal into the upstream (ADR-0017) | `cmd/api/openapi_params_test.go`, `internal/item/fuzz_test.go`, `integration/features/items.feature` |
| API-5 | Every example in `docs/curl.md` gets the status it shows. | The guide stays usable | `cmd/api/curl_test.go` |

### Security

| ID | Requirement | Why | Verified by |
| -- | ----------- | --- | ----------- |
| SEC-1 | Every route but the probes needs a valid API key: 401 without one, 403 for a read-only key's write. | Only known clients reach the data (ADR-0016) | `internal/apikey/apikey_test.go`, `cmd/api/auth_test.go`, `integration/features/auth.feature` |
| SEC-2 | Only hashes of keys are configured, compared in constant time, and no key is ever logged. | A leaked config or log reveals no usable key | `internal/apikey/apikey_test.go`, `cmd/api/logcontract_test.go` |
| SEC-3 | The service refuses to start with authentication on and no keys. | Secure by default | `cmd/api/main_test.go` |
| SEC-4 | Request headers are screened before the handlers: size limits, single-use headers, JSON bodies only; proxy and method-override headers are removed and never trusted. | Smuggling, spoofing and resource abuse through headers (ADR-0012) | `internal/headerguard/headerguard_test.go`, `internal/headerguard/fuzz_test.go`, `integration/features/headers.feature` |
| SEC-5 | Calls to the upstream and flagd never follow redirects, and credentials in their URLs never reach a log or error. | No SSRF through a compromised upstream (ADR-0019) | `internal/jsoncall/jsoncall_test.go`, `internal/item/httpstore/httpstore_test.go` |
| SEC-6 | pprof and `/metrics` are opt-in and never on the API's port; a profile runs at most 60s. | They reveal internals and cost CPU (ADR-0022) | `cmd/api/main_test.go`, `internal/profiling/profiling_test.go` |
| SEC-7 | The local stack listens on loopback only. | Nobody on a shared network reaches WireMock's admin API | Review: `compose.yaml` |
| SEC-8 | Actions and images are pinned, updated by Dependabot and scanned by govulncheck; pull request code never runs with a token that can write. | Supply chain (ADR-0020) | `.github/workflows/ci.yml` (the vuln job), `.github/dependabot.yml`, review: `.github/workflows/perf.yml` |
| SEC-9 | Every change is checked by static analysis for security mistakes (gosec) and bugs (staticcheck); a finding is fixed, or accepted on its line with the reason. | Catches what tests don't cover (ADR-0026) | `.github/workflows/ci.yml` (the lint job), review: `Makefile` |
| SEC-10 | Keys can be added, removed or rotated without a restart (`API_KEYS_FILE`); a keys file that can't be read or parsed, or holds no keys, never replaces the keys in force. A key may carry its own rate limit. | Rotation without downtime, and no lockout from a bad edit (ADR-0028) | `internal/apikey/rotation_test.go`, `cmd/api/auth_test.go`, `internal/limit/limit_test.go` |

### Reliability

| ID | Requirement | Why | Verified by |
| -- | ----------- | --- | ----------- |
| REL-1 | An upstream failure is a 502, or 504 on a timeout, never a 500 or a hang. | Callers can tell whose fault it is, and retry | `cmd/api/openapi_test.go`, `integration/features/faults.feature` |
| REL-2 | A flagd outage never fails a request: flags fall back to their embedded defaults. | Flags are an add-on (ADR-0005) | `internal/featureflag/ofrep_test.go`, `integration/features/flags.feature` |
| REL-3 | Overload is shed at once with 503 or 429 and `Retry-After`; the probes are exempt. | Protects the service and the upstream (ADR-0018) | `internal/limit/limit_test.go`, `cmd/api/openapi_test.go` |
| REL-4 | `/healthz` reports liveness; `/readyz` reports whether the upstream can be reached and the service isn't shutting down. | Orchestrators restart and route correctly (ADR-0021) | `internal/httpx/readiness_test.go`, `integration/features/health.feature` |
| REL-5 | Shutdown drains in-flight requests, after an optional delay that reports not ready. | No dropped requests on a deploy | `internal/httpx/readiness_test.go`, `internal/httpx/serve_test.go` |
| REL-6 | Every per-request resource is bounded: bodies (1 MiB), headers (the guard's limits, and twice its total in the server), upstream responses (4 MiB), metric label values. | No request can exhaust memory | `internal/httpx/json_test.go`, `cmd/api/security_test.go`, `internal/httpmetrics/httpmetrics_test.go` |

### Performance

| ID | Requirement | Why | Verified by |
| -- | ----------- | --- | ----------- |
| PERF-1 | At 200 requests a second the p99 stays under 25ms and errors under 0.1% (the `load` profile, on the local stack). | The service's latency budget | `perf/profiles.json`, `perf/profiles_test.go` |
| PERF-2 | A pull request doesn't add allocations per request, unless labelled `perf-ok`; timing, CPU and memory changes are reported. | Exact signals gate, noisy ones inform (ADR-0013) | `.github/workflows/perf.yml`, `perf/report/report_test.go` |
| PERF-3 | `main`'s performance is recorded after every merge. | Catches slow drift (ADR-0024) | `.github/workflows/perf-trends.yml`, `perf/report/trends_test.go` |

### Observability

| ID | Requirement | Why | Verified by |
| -- | ----------- | --- | ----------- |
| OBS-1 | Every log message is in the log contract, at its documented level, with its documented fields. | Logs can be queried and alerted on reliably (ADR-0006) | `internal/logx/schema_test.go`, `cmd/api/logcontract_test.go` |
| OBS-2 | Every log line of a request carries its `request_id`, and once known, its route and API key's name. | One query finds a request, and who made it (ADR-0023) | `cmd/api/auth_test.go`, `internal/apikey/apikey_test.go` |
| OBS-3 | Request rate, errors and duration per route, and the Go runtime's state, are available as metrics. | Production alerting and diagnosis (ADR-0022) | `internal/httpmetrics/httpmetrics_test.go`, `internal/profiling/metrics_test.go`, `monitoring/monitoring_test.go` |
| OBS-4 | The configuration in force is logged at startup, secrets redacted. | Know what a running instance is doing | `cmd/api/logcontract_test.go` |

### Configuration and delivery

| ID | Requirement | Why | Verified by |
| -- | ----------- | --- | ----------- |
| CFG-1 | Every setting is an environment variable with its default in code; an invalid value stops startup, with every problem reported at once. | Twelve-factor, and no silent misconfiguration (ADR-0010) | `cmd/api/config_test.go` |
| CFG-2 | The code, `.env.example` and the README agree on every setting and default. | Documentation can be trusted | `cmd/api/envexample_test.go` |
| TST-1 | Tests never start or call Docker; the integration tests talk HTTP to a stack started separately. | Fast, portable tests (ADR-0009) | Review: `integration/` |
| TST-2 | CI runs the same `make` targets a contributor runs. | What passes locally passes in CI | Review: `.github/workflows/ci.yml` |
| DEL-1 | A release is a version tag on a commit on `main`, with a section in the changelog; nothing else publishes an image. | What's released is what was reviewed and merged (ADR-0027) | `docs/changelog_test.go`, review: `.github/workflows/release.yml` |
| DEL-2 | Every released image is built for linux/amd64 and linux/arm64 from the tagged commit, with an SBOM and build provenance attached. | Users can see what's in an image and how it was made (ADR-0027) | Review: `.github/workflows/release.yml` |
| TST-3 | Code that handles untrusted input is fuzzed. | Finds what examples miss | `internal/httpx/fuzz_test.go`, `internal/headerguard/fuzz_test.go`, `internal/item/fuzz_test.go`, `internal/apikey/fuzz_test.go`, `internal/httpmetrics/fuzz_test.go` |

## Constraints and assumptions

- **TLS is terminated in front of the service.** It speaks plain HTTP; so do its calls to the
  upstream and flagd unless their URLs say `https`.
- **Limits are per instance.** With N replicas, a client may make N times `RATE_LIMIT` requests a
  second in total.
- **Behind a proxy, keyless clients share one address.** Proxy headers are never trusted
  (SEC-4), so the per-client limit sees the proxy's address for requests without a key.
- **The upstream speaks the `/items` contract** that `wiremock/mappings` stubs, and owns the
  data's consistency.

## Known gaps

What a production deployment would still want, and isn't here yet:

- **No user identities:** clients are API keys, not people or OIDC tokens.
- **Images aren't signed:** releases carry provenance, but no signature
  ([Releasing](releasing.md#not-yet)).
- **No distributed tracing:** the correlation ID covers this service only.
