# golang-vibe-starter

[![CI](https://github.com/ajlatour/golang-vibe-starter/actions/workflows/ci.yml/badge.svg)](https://github.com/ajlatour/golang-vibe-starter/actions/workflows/ci.yml)

A Go HTTP API built on the standard library only: no third-party dependencies. It serves `/items`
and gets the data from an upstream service over HTTP. In local development that upstream is
[WireMock](https://wiremock.org), serving stub responses.

```
client ──▶ api :8080 ──HTTP──▶ mock (WireMock) :8080, host :8081
           HTTPStore  │         stubs in wiremock/mappings
                      │
                      └─OFREP─▶ flagd :8016 (OpenFeature sidecar)
                                flags in flagd/flags.json
```

The upstream is set by `UPSTREAM_URL`. To use a real backend that serves the same `/items`
contract, change that variable; no code changes are needed.

## Run

```sh
docker compose up --build
curl localhost:8080/items                  # through the API
curl localhost:8081/items                  # WireMock directly
curl localhost:8081/__admin/mappings       # WireMock admin API
docker compose down
```

The API starts only after WireMock passes its healthcheck. The API image is distroless and static
(about 16 MB). It has no shell or curl, so it checks its own health with `/api healthcheck`.

To run the API outside Docker against the WireMock container:

```sh
docker compose up -d mock
UPSTREAM_URL=http://localhost:8081 go run ./cmd/api
```

Go 1.27+ is required. If your installed `go` is older, `GOTOOLCHAIN=auto` (the default) switches to
the version pinned in `go.mod`.

## Test

```sh
go test ./...
```

The tests don't need Docker. `HTTPStore` and the API are tested against an in-process upstream
(`net/http/httptest` serving the item routes over `itemtest.MemStore`, an in-memory test fake).

One opt-in test checks `HTTPStore` against the running WireMock, to catch drift between the stubs
and what the API's client expects. It's skipped unless `LIVE_UPSTREAM_URL` is set:

```sh
docker compose up -d --wait mock
LIVE_UPSTREAM_URL=http://localhost:8081 go test ./internal/item -run Live -v
```

### Integration tests (Godog)

End-to-end scenarios, written in Gherkin in `integration/features/`, run with
[Godog](https://github.com/cucumber/godog) against the whole stack: the API container built from
the Dockerfile, WireMock and flagd.

```sh
docker compose -f compose.yaml -f compose.integration.yaml up -d --build --wait
go test -C integration ./... -v
docker compose -f compose.yaml -f compose.integration.yaml down
```

- `integration/` is its own Go module. Godog is a test-only dependency, so it stays out of the
  service: the root `go.mod` has no third-party dependencies, and `go test ./...` at the root
  doesn't run these tests or need Docker.
- Test code never calls Docker: you start the stack, and the tests only talk HTTP to it and edit
  the flag definitions flagd reads.
- `compose.integration.yaml` adjusts the stack for testing: a 3s `UPSTREAM_TIMEOUT` (so the
  timeout fault gives a 504 quickly), JSON logs, and flagd reading the tests' own copy of the
  flag definitions, `integration/flagd/flags.json`, instead of `flagd/flags.json`.
- Faults: a step loads one of the `wiremock/faults/` stubs through WireMock's admin API. The
  features cover each of them (502 for unavailable, connection reset and malformed JSON, 504 for
  the timeout, 200 for the slow but in-time answer).
- Flags: a step edits the tests' copy and waits until flagd serves the new value. The suite
  resets the copy from `flagd/flags.json` when it starts, and after any scenario that changed
  it, so the copy always mirrors what ships and an interrupted run can't leave a flag switched.
  Because this edits a file, the tests must run on the same machine as the stack. flagd being
  down isn't covered here (it would mean stopping a container); the unit tests cover it. The file
  is edited in place, never replaced: flagd can lose its watch on a replaced file and stop
  reloading (see `integration/flags_test.go`).
- Correlation IDs: scenarios check that a valid `Client-Correlation-Id` is echoed back (up to
  the 128-character limit), that a missing or invalid one is replaced by a generated one, that
  generated IDs differ per request, and that error responses (404, 422, 502) carry the ID too.
  The header's name and format are read from `docs/openapi.json`, so the scenarios hold the spec
  and the service to each other. Checking the ID on log lines is left to the in-process log
  contract test, since reading the container's logs would need Docker.
- Godog's flags work with a `godog.` prefix, e.g. `-godog.tags=@smoke` for the quick checks, or
  `-godog.format=progress` for compact output.
- The suite checks the stack is up before starting, and resets WireMock before every scenario,
  so a fault left switched on can't affect a run. Scenarios run one at a time because WireMock
  faults and flag values are shared.
- WireMock doesn't store anything (see below), so scenarios check each call on its own. A create
  followed by a read of the same item can't pass against this stack.
- `API_URL`, `WIREMOCK_URL` and `FLAGD_URL` point the suite elsewhere (defaults:
  `localhost:8080`, `:8081` and `:8016`).

### Continuous integration

`.github/workflows/ci.yml` runs on every push to `main` and every pull request, in two jobs:

- **test**: `gofmt`, `go vet` (both modules) and `go test -race ./...`. Fast, no Docker.
- **integration**: starts the stack with `docker compose -f compose.yaml -f compose.integration.yaml
  up -d --build --wait`, runs the Godog suite, prints the containers' logs if anything failed, and
  always stops the stack. The commit SHA is passed as `VERSION`, so the API's log lines say which
  build they came from.

A newer push to the same branch cancels the run in progress.

To add a scenario, write it in a `.feature` file using the existing steps (listed in
`integration/steps_test.go`). An undefined step fails the run, and Godog prints a snippet to
start its definition from.

## Mock upstream (WireMock)

**Stubs** live in `wiremock/mappings/` and are mounted read-only. They serve the `/items` contract:
list, get, create, update, delete, 404 for unknown IDs, 422 for invalid names, 400 for malformed
bodies, and 405 for other methods.

- **No state is kept.** WireMock always lists `widget`, `gadget` and `gizmo`. Create returns
  `new-item`, echoing the `name` and `description` you sent, and that ID can then be updated or
  deleted. Nothing is actually stored, so a GET of `new-item` returns 404, and deletes don't
  remove anything.
- **Reads are fully static.** Create and update use response templating so the reply echoes your
  input; those requests aren't the load-test hot path.

To reload stubs after editing the files: `curl -X POST localhost:8081/__admin/mappings/reset`.

### Fault injection

Fault stubs live in `wiremock/faults/`. They aren't mounted, so none are active by default. Each
file is a single stub with a fixed `id` and the highest priority, so it overrides the matching
happy-path stub while it's loaded:

| File                    | Effect on the upstream                          | What the API does                    |
| ----------------------- | ----------------------------------------------- | ------------------------------------ |
| `slow-list.json`        | `GET /items` takes 2s                           | 200 after ~2s                        |
| `timeout-list.json`     | `GET /items` takes 8s                           | 504 after 5s (`UPSTREAM_TIMEOUT`)    |
| `unavailable.json`      | Every `/items` request returns 503              | 502, logs the upstream 503           |
| `connection-reset.json` | Every `/items` connection is reset              | 502, logs the connection reset       |
| `malformed-list.json`   | `GET /items` returns truncated JSON             | 502, logs the decode error           |

```sh
# turn a fault on
curl -X POST localhost:8081/__admin/mappings -H 'Content-Type: application/json' \
  --data @wiremock/faults/unavailable.json
# turn it off again (the ID is in the file)
curl -X DELETE localhost:8081/__admin/mappings/f0000000-0000-4000-8000-000000000003
# or drop every runtime change and go back to the mounted stubs
curl -X POST localhost:8081/__admin/mappings/reset
```

### Load-test tuning

WireMock's defaults suit unit-test-style verification, not long or high-volume runs. `compose.yaml`
sets:

- `--no-request-journal`: by default WireMock keeps every request in memory for later
  verification, and the heap grows without limit over a long run. The trade-off is that
  `/__admin/requests` and the other verification endpoints return an error. Remove the flag
  temporarily if you need to inspect requests.
- `--disable-request-logging`: no log line per request.
- `--async-response-enabled=true` and `--async-response-threads=32`: delayed responses (the slow
  and timeout faults) wait on a separate pool instead of holding Jetty request threads.
- `--container-threads=200`: a larger Jetty request thread pool, for high concurrency.
- `--disable-gzip`: no compression work on either side of a local network hop.
- `JAVA_OPTS=-Xms512m -Xmx512m`: a fixed heap, so it never pauses to resize mid-run.

The image is pinned to `wiremock/wiremock:3.13.2`, a multi-arch image that runs natively on arm64
and amd64. The `-alpine` tag is amd64-only and would run under emulation on Apple Silicon, which
distorts timings.

**Tail latency includes the mock.** WireMock runs on the JVM. The first requests run before the JIT
compiler has warmed up, and garbage-collection pauses arrive in bursts. Both show up in the API's
p99 and p99.9 as upstream latency. When load testing:

- Run a warm-up phase and leave it out of the results.
- Before blaming the API for a tail-latency spike, check whether WireMock was the cause, e.g. by
  enabling JVM GC logging through `JAVA_OPTS`.
- WireMock competes with the API and the load generator for CPU. Consider capping it with `cpus:`
  in compose, and keep the load generator's CPU usage in view.

## Feature flags (OpenFeature)

[flagd](https://flagd.dev), an OpenFeature-compliant flag service, runs next to the API as a
sidecar. The API evaluates flags over [OFREP](https://openfeature.dev/specification/appendix-c)
(the OpenFeature Remote Evaluation Protocol), which is plain HTTP/JSON, so the project stays
standard-library only. There is no OpenFeature SDK or gRPC dependency.

Flags live in `flagd/flags.json`, which flagd watches and reloads on change, with no restart.

| Flag               | Type    | Default | Effect                                              |
| ------------------ | ------- | ------- | --------------------------------------------------- |
| `items-list-total` | boolean | off     | `GET /items` also returns `"total": <item count>`.  |

```sh
curl -s -X POST localhost:8016/ofrep/v1/evaluate/flags/items-list-total -d '{"context":{}}'
# edit flagd/flags.json: set "defaultVariant" to "on", then
curl localhost:8080/items        # now includes "total"
```

Flags never fail a request. If flagd is unreachable, slow (`FLAGS_TIMEOUT`), or doesn't know the
flag, the API logs a warning and falls back to the flag's `defaultVariant` from
`flagd/flags.json`. That is the same file flagd serves, compiled into the API binary
(`flagd/embed.go`), so there is one place to define a flag and the fallback can't drift from it.
Set `FLAGS_FALLBACK_FILE` to read a different flagd-format file at startup instead (for example
one mounted from a ConfigMap). With `FLAGS_URL` unset the API skips flagd and uses those defaults
alone. Only boolean flags are supported; a flag whose default variant isn't a boolean stops the
service at startup.

To add a flag:

1. Define it in `flagd/flags.json`.
2. Declare and read it in code:
   ```go
   var FlagFoo = flags.Declare("foo") // package level
   ...
   if f.Bool(ctx, FlagFoo) { ... }   // f is the package's Flags
   ```

`flags.Declare` registers the key, and the service refuses to start if any declared flag is
missing from the definitions, so a typo or forgotten step fails fast, not at request time.

Evaluation currently sends an empty context, so targeting rules in flagd (per user, per tenant)
have nothing to match on yet. Pass request attributes in `flags.OFREP` when you need them.

## Configuration

The API reads these environment variables:

| Variable           | Default    | Description                                                |
| ------------------ | ---------- | ---------------------------------------------------------- |
| `UPSTREAM_URL`     | (required) | Base URL of the items upstream. May include a path prefix, but no query string or fragment. |
| `UPSTREAM_TIMEOUT` | `5s`       | Timeout for each upstream request.                         |
| `FLAGS_URL`        |            | flagd OFREP base URL, e.g. `http://flagd:8016`, without a query string or fragment. Unset disables flags. |
| `FLAGS_TIMEOUT`    | `500ms`    | Timeout for each flag evaluation.                          |
| `FLAGS_FALLBACK_FILE` |         | flagd-format flag file whose defaults replace the embedded `flagd/flags.json`. |
| `ADDR`             | `:8080`    | Listen address. Takes precedence over `PORT`.              |
| `PORT`             |            | Listen port, used only when `ADDR` is unset.               |
| `LOG_LEVEL`        | `info`     | `debug`, `info`, `warn`, or `error`.                       |
| `LOG_FORMAT`       | `json`     | `json` or `text`.                                          |
| `SHUTDOWN_TIMEOUT` | `10s`      | How long in-flight requests get to finish on SIGTERM.      |

## Logging

Logs are structured and go to stdout, one record per line: JSON by default (`LOG_FORMAT=json`),
`key=value` text with `LOG_FORMAT=text` for reading locally.

Every request gets a correlation ID. A caller can send one in the `Client-Correlation-Id`
header (1 to 128 characters of letters, digits and `-_.:`); otherwise the API generates one.
The ID is returned in the `Client-Correlation-Id` response header and appears as `request_id`
on every log line for that request, so a client can quote it in a bug report and every line from
that request can be found with one query.

| Field         | On                     | Meaning                                                      |
| ------------- | ---------------------- | ------------------------------------------------------------ |
| `time`        | every line             | RFC 3339 timestamp.                                          |
| `level`       | every line             | `DEBUG`, `INFO`, `WARN`, `ERROR`.                            |
| `msg`         | every line             | Fixed event name: `request received`, `response sent`, `internal error`, ... |
| `service`     | every line             | Always `api` for this binary.                                |
| `version`     | every line             | Commit that built the binary (see below), or `devel`.        |
| `request_id`  | lines within a request | Correlation ID (see above).                                  |
| `method`      | `request received`, `response sent`, error lines | HTTP method.                                |
| `route`       | `response sent`, error lines | Matched route pattern, e.g. `GET /items/{id}`. Group by this, not `path`. Empty if no route matched. Not on `request received`: routing hasn't happened yet. |
| `path`        | `request received`, `response sent`, error lines | Raw URL path. The query string is never logged. |
| `content_length` | `request received`  | Size of the request body the client declared, in bytes; -1 if unknown. The body itself is never logged. |
| `status`      | `response sent`        | Response status code.                                        |
| `bytes`       | `response sent`        | Response body size.                                          |
| `duration_ms` | `response sent`, `flag service recovered` | Time to serve the request, or the length of a flagd outage, in milliseconds (a decimal number). |
| `upstream`    | upstream call lines, `internal error` | The upstream call, `METHOD /path`, e.g. `GET /items`. |
| `upstream_status` | `upstream response received`, failure lines, `internal error` | Upstream response status. Absent if no response arrived (timeout, connection error). |
| `upstream_duration_ms` | same, not `upstream request sent` | Time spent on the upstream call, in milliseconds. |
| `error`       | error and warning lines | Error message. Always `error`, never `err`.                 |
| `panic`       | `panic serving request` | The recovered panic value.                                  |
| `stack`       | `panic serving request` | The goroutine stack at the panic.                           |
| `default`     | `flag evaluation failed, using default` | The default value that was used instead.     |
| `url`         | `using upstream`, `using feature flags` | A configured base URL, credentials redacted.  |
| `timeout`     | startup and shutdown lines | A configured timeout, e.g. `5s`.                          |
| `addr`        | `server started`       | The address the server is listening on.                      |
| `flag`        | flag lines             | Feature flag key.                                            |
| `fallbacks`   | `flag service recovered` | Evaluations served from defaults during the outage.        |

**Messages.** Every message the service logs, with the levels it uses and the fields it always
carries beyond the common ones (`time`, `level`, `msg`, `service`, `version`, and `request_id`
within a request). The first four are the four ends of a request, in order: it enters the API,
goes out to the upstream, comes back, and leaves the API.

| Message | Level | Fields | When |
| ------- | ----- | ------ | ---- |
| `request received` | INFO; DEBUG for `/healthz` | `method`, `path`, `content_length` | A request arrives. |
| `upstream request sent` | INFO | `upstream` | Before each call to the upstream. |
| `upstream response received` | INFO | `upstream`, `upstream_status`, `upstream_duration_ms` | The upstream answered, with any status. |
| `response sent` | INFO; DEBUG for `/healthz`; ERROR for 5xx | `method`, `route`, `path`, `status`, `bytes`, `duration_ms` | The reply goes to the client. |
| `internal error` | ERROR | `method`, `route`, `path`, `error` | A request failed with a 5xx. When an upstream call caused it, the `upstream*` fields are on this line too. |
| `client disconnected` | INFO | `method`, `route`, `path`, `error` | The client left mid-request; it is answered with 499, not an error. |
| `panic serving request` | ERROR | `method`, `route`, `path`, `panic`, `stack` | A handler panicked; the client gets a 500. |
| `upstream request failed` | WARN | `upstream`, `upstream_duration_ms`, `error` | An upstream call failed: no response, an unexpected status, or a body that won't decode. `upstream_status` is also present if a response arrived. |
| `upstream request cancelled` | DEBUG | `upstream`, `upstream_duration_ms`, `error` | The client left during an upstream call, so it isn't the upstream's failure. |
| `flag service unavailable, using default flag values` | WARN | `flag`, `error` | flagd stopped answering. Logged once per outage. |
| `flag service recovered` | INFO | `fallbacks`, `duration_ms` | flagd answers again; `fallbacks` evaluations were served from defaults. |
| `flag evaluation failed, using default` | WARN the first time a flag has a problem, then DEBUG | `flag`, `default`, `error` | A flag couldn't be evaluated and its default was used. Also DEBUG for each failed evaluation during an outage. |
| `flag has no configured default, using false` | ERROR | `flag` | A flag the code reads has no default in the definitions. Logged once per flag. |
| `using upstream` | INFO | `url`, `timeout` | Startup. |
| `using feature flags` | INFO | `url`, `timeout` | Startup, with `FLAGS_URL` set. |
| `feature flags: no FLAGS_URL, using default values only` | INFO | none | Startup, with `FLAGS_URL` unset. |
| `server started` | INFO | `addr` | The server is listening. |
| `shutting down` | INFO | `timeout` | A shutdown signal arrived. |
| `server stopped` | INFO | none | Shutdown finished cleanly. |

A test fails the build if this table disagrees with the code or with `logtest.Contract`, in
either direction: a message missing here, a level or field that differs, or a message the code
no longer logs. The field table above is checked against the `Key` constants the same way.

All four carry the same `request_id`, so filtering on it shows the whole exchange. The two
"received/sent" pairs bracket the work, so a request that hangs shows as a `request received`
with no `response sent`, and a stuck upstream call as an `upstream request sent` with no
`upstream response received`. A request that doesn't touch the upstream (`GET /healthz`) has just
the outer pair, and a handler may make several upstream calls. Only metadata is logged, never
headers or bodies (see the rules below).

**Levels.** All four events are `INFO`, except that `/healthz` is `DEBUG` so the container's probe
doesn't appear unless `LOG_LEVEL=debug`. That is four lines for a request that makes one upstream
call. `response sent` is `ERROR` for 5xx responses, including the 502 and 504 an upstream failure
produces (4xx are the client's mistake, not an incident), so alerting on `level=ERROR` finds
server and upstream failures alike; a failing healthcheck is still
`ERROR`. A client that disconnects mid-request logs `client disconnected` at `INFO` and a
`response sent` line with status 499, not an error.

**Upstream failures.** `upstream response received` is logged whenever any response came back, a
JSON 404 ("no such item") and a 503 alike. If the call then failed (an unexpected status, a body
that won't decode) an `upstream request failed` line at `WARN` follows, with `error`. If no
response came back at all (timeout, connection refused) there is no `received` line, only the
`WARN`, and `upstream_status` is absent. The `internal error` line a failure produces carries the
same `upstream`, `upstream_status` and `upstream_duration_ms` fields, so one line is enough to see
what the upstream did and how long it took: a 503 with a 2 ms duration is the upstream refusing,
a missing `upstream_status` with a duration near `UPSTREAM_TIMEOUT` is a timeout. The inbound
request's `method`, `path` and `status` never share a name with these, so the two can't be
confused. A client disconnecting mid-call is logged at `DEBUG` (`upstream request cancelled`), not
as an upstream failure.

**Feature flags** log without flooding: when flagd stops answering the API logs one `WARN`
(`flag service unavailable, using default flag values`) and keeps serving defaults, then one
`INFO` (`flag service recovered`, with `fallbacks`, the number of evaluations served from defaults,
and `duration_ms`, how long the outage lasted) when it's back. A problem with a single flag logs
one `WARN` until it evaluates again. Per-evaluation detail is `DEBUG`.

### Rules for log lines

What is never logged:

- Request or response bodies. The one exception is the upstream's own `error` message, quoted in
  an upstream failure and capped at 200 characters.
- Query strings. `path` is the URL path only, and the base URLs in config (`UPSTREAM_URL`,
  `FLAGS_URL`) are rejected at startup if they contain a query string or fragment.
- Headers, except the correlation ID, which is validated first (see above). `Authorization`,
  `Cookie` and everything else are never read by the logging code.
- URL credentials. A URL goes into a log only through `(*url.URL).Redacted()`, and any error that
  can come from `http.Client.Do` goes through `logx.RedactURLError` first, because net/http puts
  the URL in the error text.

Bounds: `path` and `upstream` paths are cut at 512 characters, and the upstream and flagd error
messages at 200, with a trailing `…`. Use `logx.Truncate` for any other logged value that comes
from outside the service.

To add a log line:

1. Log with a `*Context` method (`InfoContext`, `LogAttrs(ctx, ...)`) so it gets `request_id`. A
   line without the request's context can't be correlated.
2. Use the schema's field names (`error`, `duration_ms`, `route`, ...) through the `Key`
   constants in `internal/logx/keys.go`, which are the one list of field names; a test fails the
   build on a raw string key. Add a constant if the field is new. Use `logx.DurationMS` for
   durations.
3. Pick the level by who should act: `ERROR` for the service's own failures, `WARN` for a
   dependency misbehaving, `INFO` for normal events, `DEBUG` for per-call detail.
4. Add the message to `logtest.Contract` (`internal/logtest/contract.go`) with its levels and
   required fields, and to the Messages table above. Tests run the whole service and check every
   line against the contract, and check the code and this README against it: they fail on a
   message the contract doesn't know, a stale entry, missing fields, a wrong level, a missing
   `request_id`, forbidden field names (`err`, `duration`), planted secrets (in bodies, query
   strings, headers and URL credentials), unredacted URLs, unbounded lines, or a README that has
   drifted.

`version` comes from the commit Go stamps into the binary. The Docker build doesn't include
`.git`, so pass it in: `VERSION=$(git rev-parse --short HEAD) docker compose up --build`. Without
it, container logs show `version=devel`.

## Endpoints

| Method | Path          | Success | Description                                   |
| ------ | ------------- | ------- | --------------------------------------------- |
| GET    | `/healthz`    | 200     | Liveness: `{"status":"ok"}`.                  |
| GET    | `/items`      | 200     | List items: `{"items":[...]}`, oldest first.  |
| POST   | `/items`      | 201     | Create an item. Sets a `Location` header.     |
| GET    | `/items/{id}` | 200     | Get one item.                                 |
| PUT    | `/items/{id}` | 200     | Replace an item's `name` and `description`.   |
| DELETE | `/items/{id}` | 204     | Delete an item.                               |

The full contract is in [`docs/openapi.json`](docs/openapi.json), an OpenAPI 3.0 spec: every route,
request and response schema, status code and header, including the `Client-Correlation-Id`
header. Open it in any OpenAPI viewer (for example Swagger Editor, or `npx @redocly/cli
preview-docs docs/openapi.json`). It is JSON rather than YAML because the project is standard
library only, and Go has no YAML parser.

The spec can't drift from the code: `cmd/api/openapi_test.go` runs the real handlers and fails
if a route is served but not documented (or the reverse), if an answer has a status the spec
doesn't list, if a documented status is never produced, if a response body or header doesn't match
its schema, if `item.Item` and the `Item` schema disagree about fields, or if the limits stated in
the spec (the 200-character name, the correlation ID format) differ from what the service
enforces. When you change the API, update `docs/openapi.json` in the same change; the failing
test says what to fix.

`items` is a placeholder resource standing in for the real domain. Request bodies are JSON
(`{"name": "...", "description": "..."}`). `name` is required, at most 200 characters, and
trimmed of surrounding whitespace. Bodies are limited to 1 MiB, and unknown fields are rejected.

Errors are JSON, `{"error":"..."}`:

- 400: malformed body
- 404: unknown ID
- 422: validation failure
- 500: unexpected failure in the API itself; details are logged, not returned
- 502: the upstream couldn't give a usable answer: connection refused or reset, an error status,
  or a body that won't decode. The body is just `{"error":"bad gateway"}`; the upstream's message
  is logged, not returned
- 504: the upstream didn't answer within `UPSTREAM_TIMEOUT` (`{"error":"gateway timeout"}`)

## Layout

```
cmd/api/                 API service: config, wiring, Dockerfile
internal/logx/           Logging toolkit: logger, the LogKey field names, request-ID
                         context, redaction and truncation helpers. Imports nothing
                         from this module.
internal/httpx/          HTTP plumbing: env config, graceful Serve, middleware
                         (correlation IDs, request logging), JSON helpers, probe
internal/item/           Item model, Store interface, /items handlers,
                         HTTPStore and UpstreamError (the API's upstream client)
internal/flags/          OFREP client for the flagd sidecar, flag declarations,
                         and fallback defaults derived from flagd/flags.json
internal/jsoncall/        One JSON-over-HTTP call: encode, send, redact, cap, decode, and
                         classify failures. Used by HTTPStore and the flags client.
internal/item/itemtest/  MemStore: in-memory Store, used only by tests
internal/logtest/        Capture and parse JSON logs in tests
internal/nettest/        Dead, hanging, dropping and failing upstream stubs for tests
flagd/                   Feature flag definitions: served by flagd, and embedded
                         in the API as its fallback values
wiremock/mappings/       WireMock stubs (mounted)
wiremock/faults/         Opt-in fault stubs (loaded via the admin API)
docs/openapi.json        OpenAPI spec of the API, checked against the handlers by tests
integration/             Godog end-to-end tests against the composed stack (own Go module)
compose.integration.yaml Compose overrides for the integration tests
.github/workflows/       CI: unit tests, then the integration tests against the stack
compose.yaml             Local stack: api + WireMock + flagd
```

The handlers in `internal/item` depend only on the `Store` interface. To add a new backing store,
implement `Store` and run it through `testStore` in `internal/item/store_test.go`, the contract
that `HTTPStore` and the test fake both pass.
