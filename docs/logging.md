# Logging

How the service logs: the fields every line can carry, every message it writes, the levels it
uses, and the rules for adding a log line. The two tables here are checked by tests against the
code (see [the end of Messages](#messages)), so they are always current.

- [Format and correlation](#format-and-correlation)
- [Fields](#fields)
- [Messages](#messages)
- [How to read a request](#how-to-read-a-request)
- [Rules for log lines](#rules-for-log-lines)

## Format and correlation

Logs are structured and go to stdout, one record per line: JSON by default (`LOG_FORMAT=json`),
`key=value` text with `LOG_FORMAT=text` for reading locally.

Every request gets a correlation ID. A caller can send one in the `Client-Correlation-Id`
header (1 to 128 characters of letters, digits and `-_.:`); otherwise the API generates one.
The ID is returned in the `Client-Correlation-Id` response header and appears as `request_id`
on every log line for that request, so a client can quote it in a bug report and every line from
that request can be found with one query.

`version` names the build. `make up` and `make run` stamp the current commit automatically, and CI
passes the commit SHA. With plain `docker compose`, pass it in yourself:
`VERSION=$(git rev-parse --short HEAD) docker compose up --build`, because the Docker build doesn't
include `.git`; without it, container logs show `version=devel`.

## Fields

| Field         | On                     | Meaning                                                      |
| ------------- | ---------------------- | ------------------------------------------------------------ |
| `time`        | every line             | RFC 3339 timestamp.                                          |
| `level`       | every line             | `DEBUG`, `INFO`, `WARN`, `ERROR`.                            |
| `msg`         | every line             | Fixed event name: `request received`, `response sent`, `internal error`, ... |
| `service`     | every line             | Always `api` for this binary.                                |
| `version`     | every line             | Commit that built the binary (see above), or `devel`.        |
| `request_id`  | lines within a request | Correlation ID (see above).                                  |
| `trace_id`    | lines within a request | The request's W3C trace ID: the caller's, from a valid `traceparent` header, else a new one. Outbound calls carry the trace on, so it ties these lines to the trace across services. |
| `method`      | `request received`, `response sent`, error lines | HTTP method.                                |
| `route`       | `response sent`, error lines | Matched route pattern, e.g. `GET /items/{id}`. Group by this, not `path`. Empty if no route matched. Not on `request received`: routing hasn't happened yet. |
| `path`        | `request received`, `response sent`, error lines | Raw URL path. The query string is never logged. |
| `content_length` | `request received`  | Size of the request body the client declared, in bytes; -1 if unknown. The body itself is never logged. |
| `client_ip`   | `request received`, `response sent`, `request rejected by the server` | The IP address the request's connection came from: behind a load balancer, the balancer's, since proxy headers are never trusted. Off with `LOG_CLIENT_IP=off`. |
| `status`      | `response sent`        | Response status code.                                        |
| `response_code` | `response sent`, when the response was an error | The error's code (`httpx.Code`), e.g. `rate_limited`: what it means, stable, so group and alert on this rather than on `response_error`. |
| `write_error` | `response sent`, when the client didn't get the whole response | Why: `client closed the connection` (it left before the response could be sent), a failed write's error, `response aborted after a panic` (the handler panicked after the response had started, so the connection was dropped rather than a 500 appended to it), or `response aborted`. The `status` is then what the response began with, not what arrived. |
| `response_error` | `response sent`, when the response was an error | The error message the client was sent (`{"error": "..."}`): why the request was refused, or what failed. Capped at 200 characters, since it can quote the client's own input. |
| `bytes`       | `response sent`        | Response body size.                                          |
| `duration_ms` | `response sent`, `flag service recovered`, `request rejected by the server` | Time to serve the request, the length of a flagd outage, or how long a rejected request's connection was reading it, in milliseconds (a decimal number). |
| `upstream`    | upstream call lines, `internal error` | The upstream call, `METHOD /path`, e.g. `GET /items`. |
| `upstream_status` | `upstream response received`, failure lines, `internal error` | Upstream response status. Absent if no response arrived (timeout, connection error). |
| `attempt`     | `upstream request retried` | Which try the retried GET is about to make: `2` for the first retry. |
| `retries`     | `using upstream`       | How often a GET to the upstream may be retried after a fast failure (`UPSTREAM_RETRIES`). |
| `upstream_duration_ms` | same, not `upstream request sent` | Time spent on the upstream call, in milliseconds. |
| `error`       | error and warning lines | Error message. Always `error`, never `err`.                 |
| `panic`       | `panic serving request` | The recovered panic value.                                  |
| `stack`       | `panic serving request` | The goroutine stack at the panic.                           |
| `default`     | `flag evaluation failed, using default` | The default value that was used instead.     |
| `url`         | `using upstream`, `using feature flags` | A configured base URL, credentials redacted.  |
| `timeout`     | startup and shutdown lines | A configured timeout, e.g. `5s`.                          |
| `addr`        | `server started`, and the profiling server's lines | The address the server is listening on. |
| `reason`      | `request headers rejected`, `request headers would be rejected` | Which header rule was broken: `header too large`, `too many headers`, `headers too large`, `duplicate header`, `missing content type` or `unsupported content type`. |
| `header`      | the same lines, when one header is to blame | The header's name, e.g. `Content-Type`.      |
| `headers`     | `request headers stripped` | The names of the headers removed, comma-separated.       |
| `api_key`     | `request refused by authentication` (a 403), and every line of a request after it was authenticated, `response sent` included | The name of the API key, as configured in `API_KEYS`; never the key. Absent for requests without one (the probes, or with `API_AUTH=off`). |
| `api_keys`    | `using API keys`, `API keys reloaded` | The keys' names, scopes and own rate limits, e.g. `ci (write, 500/s in bursts of 1000), dashboard (read)`. |
| `reads`       | `using API keys`       | Whether reads need a key: `required` or `open` (`API_AUTH_READS`). |
| `max_in_flight` | `using limits`       | The cap on requests being served at once (`MAX_IN_FLIGHT`). |
| `in_flight` | `shutdown timed out; stopping requests in flight` | How many requests were still being served when shutdown ran out of time. |
| `rate_limit`  | `using limits`         | Requests per second each client may make on average (`RATE_LIMIT`). |
| `rate_burst`  | `using limits`         | Requests a client may make at once after a quiet spell (`RATE_BURST`). |
| `mode`        | `using header guard`   | `enforce` or `report` (`HEADER_GUARD_MODE`).                 |
| `max_headers` | `using header guard`   | The header count limit in force (`HEADER_MAX_COUNT`).        |
| `max_header_bytes` | `using header guard` | The per-header size limit in force, in bytes (`HEADER_MAX_BYTES`). |
| `max_total_bytes` | `using header guard` | The total header size limit in force, in bytes (`HEADER_MAX_TOTAL_BYTES`). |
| `flag`        | flag lines             | Feature flag key.                                            |
| `fallbacks`   | `flag service recovered` | Evaluations served from defaults during the outage.        |
| `cache_ttl`   | `using feature flags` | How long a flag's value is kept (`FLAGS_CACHE_TTL`), e.g. `1s`. |
| `fail_open`   | `using request validation`, `request validation unavailable` | Whether requests go through when the validation service can't answer (`VALIDATOR_FAILURE_MODE=open`), or are refused (`closed`, the default). |
| `enforcing`   | `request validation unavailable` | Whether the validation service's answers were being acted on (the `request-validation-enforce` flag), or only logged (shadow mode). |
| `failures`    | `request validation recovered` | Requests answered with the failure mode (refused, or let through unchecked) during the outage. |
| `go_version`  | `using runtime`        | The Go version the binary was built with, e.g. `go1.27.0`.  |
| `gomaxprocs`  | `using runtime`        | How many CPUs the Go scheduler uses at once (`GOMAXPROCS`). |
| `memory_limit_bytes` | `using runtime` | The Go runtime's soft memory limit; `0` when there's none. |
| `memory_limit_source` | `using runtime` | Where it came from: `GOMEMLIMIT`; `container`, 90% of the container's memory limit (its cgroup's), when `GOMEMLIMIT` isn't set; or `none`. |
| `task` | `panic in background task` | Which background task panicked: `runtime stats`, `API keys reload` or `profiling server`. |
| `stats_interval` | `using runtime`     | How often `runtime stats` is logged (`RUNTIME_STATS_INTERVAL`); `0s` when it isn't. |
| `heap_bytes`  | `runtime stats`        | Bytes held by heap objects: live ones, and dead ones not yet swept. |
| `runtime_memory_bytes` | `runtime stats` | All the memory the Go runtime holds from the OS: the heap, stacks, and what it's kept but not returned. Close to the process's resident memory. |
| `goroutines`  | `runtime stats`        | Goroutines running or waiting.                               |
| `gc_cycles`   | `runtime stats`        | Garbage collections since the process started.               |

## Messages

Every message the service logs, with the levels it uses and the fields it always carries beyond
the common ones (`time`, `level`, `msg`, `service`, `version`, and `request_id` within a request).
The first four are the four ends of a request, in order: it enters the API, goes out to the
upstream, comes back, and leaves the API.

| Message | Level | Fields | When |
| ------- | ----- | ------ | ---- |
| `request received` | INFO; DEBUG for `/healthz` | `method`, `path`, `content_length` | A request arrives. With `LOG_CLIENT_IP` on (the default), this and `response sent` add `client_ip`. |
| `upstream request sent` | INFO | `upstream` | Before each call to the upstream. |
| `upstream response received` | INFO | `upstream`, `upstream_status`, `upstream_duration_ms` | The upstream answered, with any status. |
| `response sent` | INFO; DEBUG for `/healthz`; ERROR for 5xx | `method`, `route`, `path`, `status`, `bytes`, `duration_ms` | The reply goes to the client. An error response adds `response_error`, what the client was told; one the client didn't get in full adds `write_error`. Logged for every request the handler gets, aborted ones included. |
| `internal error` | ERROR | `method`, `route`, `path`, `error` | A request failed with a 5xx. When an upstream call caused it, the `upstream*` fields are on this line too. |
| `client disconnected` | INFO | `method`, `route`, `path`, `error` | The client left mid-request; it is answered with 499, not an error. |
| `request stopped by shutdown` | WARN | `method`, `route`, `path`, `error` | Shutdown ran out of time (`SHUTDOWN_TIMEOUT`) with this request still running, and stopped it: it's answered 503, `shutting_down`, with `Retry-After`, so the client can retry on another instance. |
| `panic serving request` | ERROR | `method`, `route`, `path`, `panic`, `stack` | A handler panicked; the client gets a 500. |
| `request headers rejected` | INFO | `reason`, `status` | A request broke a header rule and was answered with `status` (400, 415 or 431); `header` names the header when one is to blame. See [request headers](../README.md#request-headers). |
| `request headers would be rejected` | INFO | `reason`, `status` | The same, with `HEADER_GUARD_MODE=report`: logged, but let through. |
| `API keys reloaded` | INFO | `api_keys` | `API_KEYS_FILE` changed, and its keys are now the ones in force. |
| `API keys not reloaded` | ERROR | `error` | `API_KEYS_FILE` changed but can't be read, doesn't parse or holds no keys: the keys in force stay. Logged once per problem. |
| `request refused by authentication` | INFO | `reason`, `status` | A request had no valid API key (401: `missing key`, `not a bearer token`, `unknown key`) or a read-only key tried to write (403: `read-only key`, with `api_key` naming it). The key itself is never logged. |
| `request headers stripped` | DEBUG | `headers` | Proxy or method-override headers were removed before the handlers ran. |
| `upstream request failed` | WARN | `upstream`, `upstream_duration_ms`, `error` | An upstream call failed: no response, an unexpected status, or a body that won't decode. `upstream_status` is also present if a response arrived. |
| `request rejected by the server` | WARN | `duration_ms` | The server answered a request itself, before the handler, so it has no `request received` or `response sent`: one it couldn't parse (400), headers over twice the guard's limit (431), an unsupported `Transfer-Encoding` (501), or headers that didn't arrive within 5 seconds (no answer). Also counted in `http_requests_rejected_total`. |
| `upstream request retried` | INFO | `upstream`, `upstream_duration_ms`, `attempt`, `error` | A GET the upstream gave no usable answer to, fast (refused, reset, or a 5xx), is tried again, within the retry budget. The attempt before it logged its response, if it had one. |
| `upstream request cancelled` | DEBUG | `upstream`, `upstream_duration_ms`, `error` | The client left during an upstream call, so it isn't the upstream's failure. |
| `flag service unavailable, using default flag values` | WARN | `flag`, `error` | flagd stopped answering. Logged once per outage. |
| `flag service recovered` | INFO | `fallbacks`, `duration_ms` | flagd answers again; `fallbacks` evaluations were served from defaults. |
| `flag evaluation failed, using default` | WARN the first time a flag has a problem, then DEBUG | `flag`, `default`, `error` | A flag couldn't be evaluated and its default was used. Also DEBUG for each failed evaluation during an outage. |
| `flag has no configured default, using false` | ERROR | `flag` | A flag the code reads has no default in the definitions. Logged once per flag. |
| `using upstream` | INFO | `url`, `timeout`, `retries` | Startup. |
| `using feature flags` | INFO | `url`, `timeout`, `cache_ttl` | Startup, with `FLAGS_URL` set. |
| `using API keys` | INFO | `api_keys`, `reads` | Startup: the names and scopes of the configured keys (never the keys), and whether reads need one. |
| `authentication is off: every request is allowed` | WARN | none | Startup, with `API_AUTH=off`. For local development only. |
| `using limits` | INFO | `max_in_flight`, `rate_limit`, `rate_burst` | Startup: the cap on requests in flight and the per-client rate limit in force (0 is none). |
| `using header guard` | INFO | `mode`, `max_headers`, `max_header_bytes`, `max_total_bytes` | Startup: the header rules actually in force, after any overrides. |
| `feature flags: no FLAGS_URL, using default values only` | INFO | none | Startup, with `FLAGS_URL` unset. |
| `server started` | INFO | `addr` | The server is listening. |
| `shutting down` | INFO | `timeout` | A shutdown signal arrived. |
| `shutdown timed out; stopping requests in flight` | WARN | `in_flight` | `SHUTDOWN_TIMEOUT` passed with requests still running. Each is stopped, and answers and logs (`request stopped by shutdown`) within 2 seconds; any still running then are cut off, and `service failed` says how many. |
| `not ready` | WARN | `reason` | `/readyz` started answering 503: `shutting down`, or a check failed (`upstream unavailable`, with `error`). Logged once per change, not per probe. |
| `ready again` | INFO | none | A failing readiness check passes again. |
| `server stopped` | INFO | none | Shutdown finished cleanly. |
| `service failed` | ERROR | `error` | The service stopped on an error: a configuration error at startup, a port it couldn't listen on, or a shutdown that ran out of time. The last line it writes. |
| `using request validation` | INFO | `url`, `timeout`, `fail_open` | Startup: every request but the probes is checked against the validation service. |
| `request validation is off: no VALIDATOR_URL; every request goes unchecked` | WARN | none | Startup, without `VALIDATOR_URL`. Fine locally; in production, the check isn't happening. |
| `request refused by validation` | INFO | `reason` | The validation service refused a request: a 403. `reason` is the service's, never sent to the client. |
| `request would be refused by validation` | INFO | `reason` | Shadow mode (the `request-validation-enforce` flag off): the validation service refused a request, which went through anyway. |
| `request validation unavailable` | WARN | `enforcing`, `fail_open`, `error` | The validation service stopped answering: once per outage. Until it's back, requests are refused (503) or, failing open or in shadow mode, let through unchecked, and the service is asked again once a second. |
| `request validation recovered` | INFO | `failures`, `duration_ms` | The validation service answers again: how many requests the outage cost, and how long it lasted. |
| `using runtime` | INFO | `go_version`, `gomaxprocs`, `memory_limit_bytes`, `memory_limit_source`, `stats_interval` | Startup: what the Go runtime runs with. |
| `panic in background task` | ERROR | `task`, `panic`, `stack` | A task beside the requests panicked. It's stopped, and the service carries on without it (without reloading API keys, say), where an unrecovered panic would have ended the process and every request in flight. Alert on it. |
| `runtime stats` | INFO | `heap_bytes`, `runtime_memory_bytes`, `goroutines`, `gc_cycles` | Every `RUNTIME_STATS_INTERVAL` (5m): what the runtime holds. See [Memory over time](#memory-over-time). |
| `profiling server started` | INFO | `addr` | Startup, with `PPROF_ADDR` set: the runtime profiles are served there. |
| `profiling server is reachable beyond this machine; keep its port private` | WARN | `addr` | Startup, with `PPROF_ADDR` on an address other machines can reach. Expected inside a container whose port is published privately, as in the compose stack; anywhere else, a mistake. |
| `profiling server failed` | ERROR | `error` | The profiling server stopped unexpectedly. The API keeps running, without profiles. |

These two tables are checked against the code. A test fails the build if the Messages table
disagrees with the code or with `logtest.Contract` (`internal/logtest/contract.go`), in either
direction: a message missing here, a level or field that differs, or a message the code no longer
logs. The Fields table is checked against the `Key` constants in `internal/logx/keys.go` the same
way. The tests are in `internal/logx/schema_test.go`.

## How to read a request

**The four ends.** `request received`, `upstream request sent`, `upstream response received` and
`response sent` all carry the same `request_id`, so filtering on it shows the whole exchange. The
two received/sent pairs bracket the work, so a request that hangs shows as a `request received`
with no `response sent`, and a stuck upstream call as an `upstream request sent` with no
`upstream response received`. A request that doesn't touch the upstream (`GET /healthz`) has just
the outer pair, and a handler may make several upstream calls. Only metadata is logged, never
headers or bodies (see the rules below).

**Levels.** All four are `INFO`, except that `/healthz` is `DEBUG` so the container's probe doesn't
appear unless `LOG_LEVEL=debug`. That is four lines for a request that makes one upstream call.
`response sent` is `ERROR` for 5xx responses, including the 502 and 504 an upstream failure
produces (4xx are the client's mistake, not an incident), so alerting on `level=ERROR` finds
server and upstream failures alike; a failing healthcheck is still `ERROR`. A client that
disconnects mid-request logs `client disconnected` at `INFO` and a `response sent` line with
status 499, not an error.

**Requests the server rejects itself.** Every request that reaches the handler is logged as above,
whatever happens to it. A few never get that far: the HTTP server answers them itself, so no
`request received` or `response sent` is written. Each is logged as one `request rejected by the
server` line at `WARN` instead, with `client_ip` and how long the connection spent reading it,
but no `request_id`, method or path, which the server never got to (and doesn't report). See its
row above for what these are; a burst of them usually means a misbehaving client or a scanner.

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

**Feature flags** log without flooding: when flagd stops answering, the API logs one `WARN`
(`flag service unavailable, using default flag values`) and keeps serving defaults, then one
`INFO` (`flag service recovered`, with `fallbacks`, the number of evaluations served from
defaults, and `duration_ms`, how long the outage lasted) when it's back. A problem with a single
flag logs one `WARN` until it evaluates again. Per-evaluation detail is `DEBUG`.

## Memory over time

Where the logs are all there is, `runtime stats` is the view of memory: one line every
`RUNTIME_STATS_INTERVAL` (5m), with what the Go runtime holds. Read it as a trend over hours,
never as one line:

- **A leak** is `heap_bytes` or `goroutines` whose low points keep rising while traffic doesn't.
  Growing goroutines are the more common leak in Go: something blocked forever, holding its stack
  and what it references.
- **Memory the runtime keeps** is `runtime_memory_bytes` growing while `heap_bytes` doesn't: the
  runtime hasn't returned freed memory to the OS yet. That's not a leak; `GOMEMLIMIT` bounds it.
- **Pressure** is `gc_cycles` rising quickly between lines: the collector is working hard to stay
  inside the memory limit.

`using runtime` at startup says what limits applied: `memory_limit_bytes`, from `GOMEMLIMIT` or,
without it, 90% of the container's memory limit (`memory_limit_source`), so the collector works
harder before the kernel's out-of-memory killer would end the process. And
`service failed` is the last line if the service stops on an error. To find which code holds the
memory, profiles are the next step: [Performance](performance.md#profiling).

## Rules for log lines

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

What is logged that may be personal data: `client_ip`, the address each request came from, and
`api_key`, a key's configured name. An IP address is personal data in many jurisdictions;
`LOG_CLIENT_IP=off` leaves it out, where a deployment's rules say to.

Bounds: `path` and `upstream` paths are cut at 512 characters, and the upstream and flagd error
messages at 200, with a trailing `…`. Use `logx.Truncate` for any other logged value that comes
from outside the service.

To add a log line:

1. Log with a `*Context` method (`InfoContext`, `LogAttrs(ctx, ...)`) so it gets `request_id`. A
   line without the request's context can't be correlated.
2. Use the schema's field names (`error`, `duration_ms`, `route`, ...) through the `Key`
   constants in `internal/logx/keys.go`, which are the one list of field names; a test fails the
   build on a raw string key. Add a constant if the field is new, and a row to the Fields table.
   Use `logx.DurationMS` for durations.
3. Pick the level by who should act: `ERROR` for the service's own failures, `WARN` for a
   dependency misbehaving, `INFO` for normal events, `DEBUG` for per-call detail.
4. Add the message to `logtest.Contract` (`internal/logtest/contract.go`) with its levels and
   required fields, and to the Messages table above. Tests run the whole service and check every
   line against the contract, and check the code and this document against it: they fail on a
   message the contract doesn't know, a stale entry, missing fields, a wrong level, a missing
   `request_id`, forbidden field names (`err`, `duration`), planted secrets (in bodies, query
   strings, headers and URL credentials), unredacted URLs, unbounded lines, or a table here that
   has drifted.
