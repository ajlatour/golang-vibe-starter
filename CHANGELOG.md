# Changelog

Notable changes to this project. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). There are no tagged releases yet, so
everything is under Unreleased.

## Roadmap

The structured logging plan (phases 1 to 4) is complete; see Unreleased. The goal was logs that
are easy to read and to query when debugging a deployed instance. What's left is deferred:

### Deferred

- OpenTelemetry tracing (`trace_id`/`span_id`): needs third-party dependencies. The
  correlation ID covers this service; trace context can be added later through the same
  context-aware log handler.
- Changing the log level at runtime (`slog.LevelVar` and an admin endpoint).
- Log sampling.

## Unreleased

### Added

- Structured logging, phase 1:
  - Correlation IDs. Each request gets an ID from the `Client-Correlation-Id` request header (if
    valid: 1-128 characters of letters, digits and `-_.:`) or a generated one. It's returned in
    the `Client-Correlation-Id` response header and logged as `request_id` on every line logged
    with the request's context.
  - `service` and `version` on every log line. `version` is the VCS commit from the binary's
    build info, or the `VERSION` build arg for Docker images (`devel` if unset).
  - `route` (the matched pattern, e.g. `GET /items/{id}`) on request and error lines, for grouping.
  - A documented log schema in the README.
- Structured logging, phase 2:
  - Request log lines are at `ERROR` for 5xx and `INFO` otherwise, including 4xx, which are the
    client's mistake rather than an incident.
  - `/healthz` requests log at `DEBUG`, so the container probe no longer appears at the default
    level. A failing healthcheck (5xx) still logs at `ERROR`.
  - A client disconnecting mid-request is logged as `client disconnected` at `INFO` and answered
    with status 499, instead of an `internal error` at `ERROR`. Upstream timeouts remain errors.
  - flagd outages log one `WARN` (`flag service unavailable, using default flag values`) when
    they start and one `INFO` (`flag service recovered`, with `fallbacks` and `duration_ms`) when
    they end; failed evaluations in between log at `DEBUG`. A problem with a single flag (unknown,
    wrong type) logs one `WARN` for that flag until it evaluates again, and a flag with no
    configured default logs one `ERROR`.
- Structured logging, phase 3:
  - Every upstream call is logged by `HTTPStore`: `upstream request` at `DEBUG` when the upstream
    answered (including a JSON 404, which means "no such item"), `upstream request failed` at
    `WARN` with `error` when it didn't. A call abandoned because the client disconnected is
    `DEBUG`, not a failure.
  - New fields on those lines: `upstream` (`METHOD /path`), `upstream_status` (absent when there
    was no response, e.g. a timeout or connection error) and `upstream_duration_ms`.
  - A failed call returns an `*item.UpstreamError`, and the handler's `internal error` line
    includes the same three fields, so one line shows what the upstream did. Error messages are
    unchanged.
- Structured logging, phase 4:
  - Written rules for log lines in the README: what is never logged, the bounds on logged values,
    and how to add a line.
  - A log contract test (`cmd/api/logcontract_test.go`) that runs the whole service and checks
    every line against a schema table: required fields, levels, `request_id`, forbidden field
    names, planted secrets, unredacted URLs and line size. A log message missing from the table
    fails the test.
- Feature flags with a flagd (OpenFeature) sidecar, evaluated over OFREP. The demo flag
  `items-list-total` adds `"total"` to `GET /items`.
- Flag fallbacks: when flagd can't answer, the API uses each flag's `defaultVariant` from
  `flagd/flags.json`, which is embedded in the binary. Flags are declared with `flags.Declare`,
  and the service won't start if a declared flag isn't defined. `FLAGS_FALLBACK_FILE` overrides
  the embedded definitions.
- `FLAGS_URL`, `FLAGS_TIMEOUT` and `FLAGS_FALLBACK_FILE` environment variables.

### Changed

- Logging safety (phase 4):
  - `UPSTREAM_URL` and `FLAGS_URL` are rejected at startup if they contain a query string or
    fragment. Request paths are appended to these URLs, so they never worked, and a token in one
    would have appeared in logged errors.
  - Credentials in a base URL are masked in logged transport errors (`user:xxxxx@`). Before, the
    username was visible.
  - `path` and upstream paths are cut at 512 characters, and the upstream's and flagd's error
    messages at 200.

- Log field renames, which affect any existing log queries:
  - `err` is now `error` (on `internal error` lines).
  - `duration` (integer nanoseconds in JSON) is now `duration_ms` (decimal milliseconds).
  - Timeouts on startup and shutdown lines are strings such as `5s` instead of nanoseconds.
- Flag evaluation no longer logs a warning on every failed evaluation (see phase 2 above).
- Feature-flag warnings are logged with the request context, so they carry `request_id`.
