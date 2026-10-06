# golang-vibe-starter

[![CI](https://github.com/ajlatour/golang-vibe-starter/actions/workflows/ci.yml/badge.svg)](https://github.com/ajlatour/golang-vibe-starter/actions/workflows/ci.yml)

A Go HTTP API built on the standard library only: no third-party dependencies. It serves `/items`
and gets the data from an upstream service over HTTP. In local development that upstream is
[WireMock](https://wiremock.org), serving stub responses, and feature flags come from
[flagd](https://flagd.dev).

```
client ──▶ api :8080 ──HTTP──▶ mock (WireMock) :8080, host :8081
           httpstore  │         stubs in wiremock/mappings
                      │
                      └─OFREP─▶ flagd :8016 (OpenFeature sidecar)
                                flags in flagd/flags.json
```

The upstream is set by `UPSTREAM_URL`. To use a real backend that serves the same `/items`
contract, change that variable; no code changes are needed.

## Prerequisites

- Go 1.27 or later. If your installed `go` is older, `GOTOOLCHAIN=auto` (the default) switches to
  the version pinned in `go.mod`.
- Docker with Compose, to run the local stack and the integration tests. The unit tests don't
  need it.
- `make` (GNU Make 3.81 or later, already on macOS and most Linux systems) for the shortcuts below.
  Without it, each target in the `Makefile` is a few lines you can run by hand.

Nothing else: the documentation and spec tools run in Docker, and the Go tools and the
integration tests' dependencies download on first use.

## Run

```sh
make up                                    # API, WireMock and flagd, in the background
scripts/api GET /items                     # through the API (curl, with the headers filled in)
scripts/api POST /items '{"name": "Sprocket"}'
curl localhost:8081/items                  # WireMock directly
make fault FAULT=unavailable               # make the upstream fail; make fault-reset to undo
make down
```

`make help` lists every shortcut. [Calling the API](docs/curl.md) has a tested example of every
call, with `scripts/api` and with plain curl. [The local stack](docs/local-stack.md) covers running the API
outside Docker, WireMock's stubs, upstream faults, and load testing.

## Test

```sh
make test           # unit tests: fast, no Docker
make check          # what CI's fast job runs: gofmt, vet and the race-enabled unit tests
make integration    # starts the stack, runs the integration tests, stops the stack
make perf PROFILE=load   # k6 load test against the stack; see docs/performance.md
make perf-compare   # benchmarks and a load test against main: the report CI posts on a pull request
make perf-trends    # main's performance over time, from the records CI keeps
```

CI runs these same targets on every push to `main` and every pull request.
[Development](docs/development.md) explains the tests, and which documents they keep in step with
the code.

## API

| Method | Path          | Success | Description                                   |
| ------ | ------------- | ------- | --------------------------------------------- |
| GET    | `/healthz`    | 200     | Liveness: `{"status":"ok"}`.                  |
| GET    | `/readyz`     | 200/503 | Readiness: whether to send this instance traffic. |
| GET    | `/items`      | 200     | List items: `{"items":[...]}`, oldest first.  |
| POST   | `/items`      | 201     | Create an item. Sets a `Location` header.     |
| GET    | `/items/{id}` | 200     | Get one item.                                 |
| PUT    | `/items/{id}` | 200     | Replace an item's `name` and `description`.   |
| DELETE | `/items/{id}` | 204     | Delete an item.                               |

`items` is a placeholder resource standing in for the real domain. Request bodies are JSON
(`{"name": "...", "description": "..."}`). `name` is required, at most 200 characters, and
trimmed of surrounding whitespace; `description` is optional, at most 2000 characters. Bodies are limited to 1 MiB, and unknown fields are rejected.
An item ID is 1 to 128 letters, digits, `-` and `_`; no item has any other ID, so any other ID is a
404, answered by the API without asking the upstream.

`/healthz` says the process is alive: the container's healthcheck restarts it if not. `/readyz`
says whether to send it traffic: 503 once it starts shutting down (see `SHUTDOWN_DELAY`), or if it
can't reach the upstream at all. Any answer from the upstream counts, an error included, so a
struggling upstream doesn't take every instance out of service at once. Neither probe needs an API
key or counts against the limits.

Every route but the probes needs an API key, sent as `Authorization: Bearer <key>`. A key may read,
or read and write, and may carry its own rate limit. `make api-key NAME=<who> SCOPE=read|write`
generates one, with the `API_KEYS` entry that configures it; the local stack comes with two
development keys ([Calling the API](docs/curl.md#api-keys)). With `API_KEYS_FILE`, keys are read
from a file and reloaded while the service runs, so they can be rotated without a restart: add the
new key, move clients to it, then remove the old one.

Errors are JSON, `{"error":"..."}`:

- 400: malformed body, or a header sent twice that may appear only once
- 401: no API key, or not a valid one (with `WWW-Authenticate: Bearer`)
- 403: a write with a read-only API key
- 404: unknown ID
- 429: over the client's rate limit (with `Retry-After`)
- 415: a request body not sent as `Content-Type: application/json`
- 422: validation failure
- 431: request headers over a size limit
- 500: unexpected failure in the API itself; details are logged, not returned
- 502: the upstream couldn't give a usable answer: connection refused or reset, an error status,
  or a body that won't decode. The body is just `{"error":"bad gateway"}`; the upstream's message
  is logged, not returned
- 503: the service is serving as many requests as it takes at once (with `Retry-After`)
- 504: the upstream didn't answer within `UPSTREAM_TIMEOUT` (`{"error":"gateway timeout"}`)

Every response carries a `Client-Correlation-Id` header. Send your own to tie the service's log
lines to yours, or quote the one returned when reporting a problem.

Every response also carries `X-Content-Type-Options: nosniff`, so a browser never treats a body as
anything but its declared type, and `Cache-Control: no-store`, so nothing keeps a copy of it. An
error body describes the problem in JSON's terms (`field "name" must be a string`), never the
service's internals.

### Request headers

Every request's headers are screened before the handlers see them (`internal/headerguard`):

| Rule | Otherwise |
| ---- | --------- |
| A request with a body sends `Content-Type: application/json` (parameters such as `charset` are fine) | 415 |
| By default, at most 100 headers, 8 KiB for any one header (name and value), and 32 KiB in total (`HEADER_MAX_COUNT`, `HEADER_MAX_BYTES`, `HEADER_MAX_TOTAL_BYTES`) | 431 |
| `Content-Type`, `Authorization` and `Client-Correlation-Id` appear at most once | 400 |
| Proxy and method-override headers (`X-Forwarded-For`, `-Host`, `-Proto`, `Forwarded`, `X-Real-IP`, `X-HTTP-Method-Override`, `X-Method-Override`, `X-HTTP-Method`) | Removed: nothing here may trust them |

A rejected request gets a JSON error and a `Client-Correlation-Id` like any other, and a
`request headers rejected` log line saying which rule it broke. The service logs the limits
actually in force at startup (`using header guard`). Which headers are single-use or removed isn't
configurable: those are security decisions, kept in code where changes are reviewed and tested. To roll the rules out without
blocking anyone, set `HEADER_GUARD_MODE=report`: requests are let through and logged as
`request headers would be rejected` instead. Removing headers happens in both modes. Go's HTTP
server already rejects malformed headers before this runs, and anything over twice
`HEADER_MAX_TOTAL_BYTES` (request line included) with a plain 431 of its own, so no connection can
make it hold more.

The full contract is in [`docs/openapi.json`](docs/openapi.json), an OpenAPI 3.0 spec: every route,
request and response schema, status code and header. `make spec` renders it as a web page,
`bin/openapi.html`, or open it in any OpenAPI viewer. It is JSON rather than YAML because the
project is standard library only, and Go has no YAML parser. Tests run the real handlers against
it, so it can't drift from the code.

## Configuration

The API reads these environment variables:

| Variable              | Default    | Description                                                |
| --------------------- | ---------- | ---------------------------------------------------------- |
| `UPSTREAM_URL`        | (required) | Base URL of the items upstream. May include a path prefix, but no query string or fragment. |
| `UPSTREAM_TIMEOUT`    | `5s`       | Timeout for each upstream request.                         |
| `FLAGS_URL`           |            | flagd OFREP base URL, e.g. `http://flagd:8016`, without a query string or fragment. Unset disables flags. |
| `FLAGS_TIMEOUT`       | `500ms`    | Timeout for each flag evaluation.                          |
| `FLAGS_CACHE_TTL`     | `1s`       | How long a flag's value is kept after flagd serves it, so a change in flagd shows within this; during a flagd outage, how often flagd is retried, with the evaluations in between taking their defaults at once. `0s` asks flagd every time. |
| `FLAGS_FALLBACK_FILE` |            | flagd-format flag file whose defaults replace the embedded `flagd/flags.json`. |
| `ADDR`                | `:8080`    | Listen address. Takes precedence over `PORT`.              |
| `PORT`                |            | Listen port, used only when `ADDR` is unset.               |
| `LOG_LEVEL`           | `info`     | `debug`, `info`, `warn`, or `error`.                       |
| `LOG_FORMAT`          | `json`     | `json` or `text`.                                          |
| `SHUTDOWN_TIMEOUT`    | `10s`      | How long in-flight requests get to finish on SIGTERM.      |
| `SHUTDOWN_DELAY`      | `0s`       | How long to keep serving after SIGTERM, reporting not ready on `/readyz`, before refusing new connections: time for a load balancer to stop sending requests. |
| `API_KEYS`            | (required) | API keys: comma-separated `name:scope:sha256` entries, scope `read` or `write`, with an optional fourth field, the key's own rate limit (`:500` or `:500/1000` for a burst). `make api-key NAME=<who> SCOPE=read\|write` generates one. Only the key's hash goes here. Required unless `API_KEYS_FILE` is set or `API_AUTH=off`. |
| `API_KEYS_FILE`       |            | Instead of `API_KEYS`, a file of the same entries, one per line, reloaded while the service runs: keys are added, removed or rotated without a restart. A file that won't parse changes nothing. |
| `API_KEYS_RELOAD`     | `10s`      | How often `API_KEYS_FILE` is checked for changes. A change applies once the file reads the same on two checks, so within twice this. |
| `API_AUTH`            | `on`       | `off` lets every request through without a key: local development only, and logged as a warning. |
| `API_AUTH_READS`      | `required` | `open` lets reads (`GET`) through without a key; a key that is sent must still be valid. |
| `MAX_IN_FLIGHT`       | `512`      | The most requests served at once; more get 503 with `Retry-After`. `0` is no cap. |
| `RATE_LIMIT`          | `100`      | Requests per second each client may make on average; more get 429 with `Retry-After`. A client is its API key, or its IP address without one. `0` is no limit. |
| `RATE_BURST`          | `200`      | Requests a client may make at once after a quiet spell. |
| `LOG_CLIENT_IP`       | `on`       | `off` leaves the client's IP address (`client_ip`) out of the request log lines. It's the connection's address, so behind a load balancer the balancer's. An IP address is personal data in many jurisdictions. |
| `RUNTIME_STATS_INTERVAL` | `5m`   | How often to log the Go runtime's heap, memory and goroutines (`runtime stats`), the view of memory over time where the logs are all there is. `0s` logs none. |
| `PPROF_ADDR`          |            | Where to serve the runtime profiles (pprof) and metrics (`/metrics`: requests by route, and the Go runtime), e.g. `127.0.0.1:6061`. Unset serves neither. Keep it private: see [Performance](docs/performance.md#profiling). |
| `HEADER_GUARD_MODE`   | `enforce`  | `enforce` rejects requests that break a [header rule](#request-headers); `report` only logs them. |
| `HEADER_MAX_COUNT`    | `100`      | The most request headers allowed.                          |
| `HEADER_MAX_BYTES`    | `8192`     | The largest any one request header may be, name and value, in bytes. |
| `HEADER_MAX_TOTAL_BYTES` | `32768` | The largest the request headers may be in total, in bytes. |

[`.env.example`](.env.example) lists every variable with its default. To change settings when
running with docker compose, copy it to `.env` (git-ignored) and uncomment the lines you want;
compose always sets `UPSTREAM_URL` and `FLAGS_URL` itself, to wire the API to its containers.
A setting can also come from the shell, e.g. `HEADER_GUARD_MODE=report make up`. A test keeps the
code, `.env.example` and this table in step. The header limits must be whole numbers from 1 to
1048576, with `HEADER_MAX_BYTES` no larger than `HEADER_MAX_TOTAL_BYTES`; the service refuses to
start otherwise, so a typo can't switch a limit off.

## Documentation

`make docs-open` renders all of this as a local website and opens it in your browser, with the
pages grouped by the same themes as below. `make godoc` serves the Go package
documentation (the code's doc comments) at http://localhost:6060.

**Start**

- [`CHANGELOG.md`](CHANGELOG.md): what changed, release by release.

**Use the API**

- [Calling the API](docs/curl.md): `scripts/api` and plain curl examples for every route, error
  and header rule.
- [`docs/openapi.json`](docs/openapi.json): the API spec.

**Run it**

- [The local stack](docs/local-stack.md): the compose stack, WireMock's stubs, fault injection,
  and load-test tuning.
- [Feature flags](docs/feature-flags.md): the flags, what happens when flagd can't answer, and
  adding a flag.
- [Logging](docs/logging.md): every field and message, how to read a request's log lines, and the
  rules for adding a log line.

**Build and ship**

- [Development](docs/development.md): the ground rules, what to update when you change something,
  and the unit, integration and CI tests.
- [Performance](docs/performance.md): Go benchmarks, k6 load, stress, spike and soak tests,
  watching CPU and memory (with a live dashboard), and profiling with pprof.
- [Releasing](docs/releasing.md): cutting a version, and the image, SBOM and provenance it
  publishes.

**Architecture**

- [Architecture](docs/architecture.md): how the service is built, from its context to its
  packages, and its requirements, each with the test that verifies it.
- [Architecture decisions](docs/decisions.md): why it's built that way, one record per decision.

## Layout

```
cmd/api/                 API service: config, wiring, Dockerfile
cmd/apikey/              Generates an API key and its API_KEYS entry (make api-key)
internal/item/           Item model, Store interface, /items handlers
internal/item/httpstore/ The production Store: items on the upstream over HTTP,
                         and UpstreamError (502/504) when the upstream fails
internal/httpx/          HTTP plumbing: graceful Serve, middleware (correlation IDs,
                         request logging), JSON helpers, the health and readiness probes
internal/headerguard/    Screens every request's headers: limits, duplicates, JSON
                         bodies, and stripping proxy and override headers
internal/apikey/         API key authentication: hashed keys, read and write scopes
internal/limit/          The cap on requests in flight, and each client's rate limit
internal/httpmetrics/    Request metrics per route, in Prometheus's text format
internal/envconfig/      Reading configuration from environment variables, and the
                         settings every service shares (address, logging, shutdown)
internal/logx/           Logging toolkit: logger, the Key field names, request-ID
                         context, redaction and truncation helpers. Imports nothing
                         from this module.
internal/featureflag/    OFREP client for the flagd sidecar, flag declarations,
                         and fallback defaults derived from flagd/flags.json
internal/jsoncall/       One JSON-over-HTTP call: encode, send, redact, cap, decode, and
                         classify failures. Used by httpstore and featureflag.
internal/item/itemtest/  MemStore (an in-memory Store) and StoreContract, for tests
internal/profiling/      Serves pprof and runtime metrics on their own listener (PPROF_ADDR)
internal/logtest/        Capture and parse JSON logs in tests; the log contract
internal/nettest/        Dead, hanging, dropping and failing upstream stubs for tests
scripts/api              Calls the API with curl; see docs/curl.md
scripts/build-api-image.sh  Builds the API's image, keeping Go's build cache (CI)
scripts/changelog.sh     Cuts a release's changelog section, and prints its notes
perf/                    k6 load profiles, the scripts that run them, and the PR report
monitoring/              Prometheus and Grafana config for make monitor
flagd/                   Feature flag definitions: served by flagd, and embedded
                         in the API as its fallback values
wiremock/mappings/       WireMock stubs (mounted)
wiremock/faults/         Opt-in fault stubs (loaded via the admin API)
docs/                    The documents above, the OpenAPI spec, and site/, which
                         renders them as a local website (make docs)
Makefile                 Shortcuts for running, testing and checking (make help)
integration/             Godog end-to-end tests against the composed stack (own Go module)
compose.yaml             Local stack: api + WireMock + flagd
compose.integration.yaml Compose overrides for the integration tests (ignores .env)
.env.example             Every setting the API reads, with its default
.github/workflows/       CI, the pull request performance report, main's performance record,
                         and releases
```
