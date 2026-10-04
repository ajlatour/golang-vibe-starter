# golang-vibe-starter

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
| `flag`        | flag lines             | Feature flag key.                                            |
| `fallbacks`   | `flag service recovered` | Evaluations served from defaults during the outage.        |

**The four ends of a request.** Each request is logged where it enters and leaves the API, and
where it enters and leaves the upstream, in this order:

| Event                    | `msg`                        | Fields beyond the common ones                                  |
| ------------------------ | ---------------------------- | -------------------------------------------------------------- |
| Request arrives          | `request received`           | `method`, `path`, `content_length`                             |
| Request goes upstream    | `upstream request sent`      | `upstream`                                                     |
| Response from upstream   | `upstream response received` | `upstream`, `upstream_status`, `upstream_duration_ms`          |
| Response goes to client  | `response sent`              | `method`, `route`, `path`, `status`, `bytes`, `duration_ms`    |

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
  can come from `http.Client.Do` goes through `httpx.RedactURLError` first, because net/http puts
  the URL in the error text.

Bounds: `path` and `upstream` paths are cut at 512 characters, and the upstream and flagd error
messages at 200, with a trailing `…`. Use `httpx.Truncate` for any other logged value that comes
from outside the service.

To add a log line:

1. Log with a `*Context` method (`InfoContext`, `LogAttrs(ctx, ...)`) so it gets `request_id`. A
   line without the request's context can't be correlated.
2. Use the schema's field names (`error`, `duration_ms`, `route`, ...) through the `LogKey`
   constants in `internal/httpx/log.go`, which are the one list of field names; a test fails the
   build on a raw string key. Add a constant if the field is new. Use `httpx.DurationMS` for
   durations.
3. Pick the level by who should act: `ERROR` for the service's own failures, `WARN` for a
   dependency misbehaving, `INFO` for normal events, `DEBUG` for per-call detail.
4. Add the message to `logContract` in `cmd/api/logcontract_test.go` with its level and required
   fields. The test runs the whole service and fails on any message it doesn't know, on missing
   fields, wrong levels, a missing `request_id`, forbidden field names (`err`, `duration`), planted
   secrets (in bodies, query strings, headers and URL credentials), unredacted URLs, and
   unbounded lines. Update the table above to match.

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
internal/httpx/          HTTP plumbing: env config, graceful Serve, middleware,
                         JSON helpers, healthcheck probe
internal/item/           Item model, Store interface, /items handlers,
                         HTTPStore (the API's upstream client)
internal/item/itemtest/  MemStore: in-memory Store, used only by tests
internal/flags/          OFREP client for the flagd sidecar, flag declarations,
                         and fallback defaults derived from flagd/flags.json
flagd/                   Feature flag definitions: served by flagd, and embedded
                         in the API as its fallback values
wiremock/mappings/       WireMock stubs (mounted)
wiremock/faults/         Opt-in fault stubs (loaded via the admin API)
compose.yaml             Local stack: api + WireMock + flagd
```

The handlers in `internal/item` depend only on the `Store` interface. To add a new backing store,
implement `Store` and run it through `testStore` in `internal/item/store_test.go`, the contract
that `HTTPStore` and the test fake both pass.
