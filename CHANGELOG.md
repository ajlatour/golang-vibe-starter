# Changelog

Notable changes to this project. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). There are no tagged releases yet, so
everything is under Unreleased.

## Roadmap

Planned work for structured logging. Phase 1 is done (see Unreleased); the rest is not started.
The goal is logs that are easy to read and to query when debugging a deployed instance.

### Phase 2: correct levels and less noise

- Set the request log level from the status: 5xx at `ERROR`, everything else at `INFO`.
- Log `/healthz` requests at `DEBUG`, so the container probe (every 10s) doesn't bury real
  traffic.
- Treat a client disconnecting mid-request (`context.Canceled` from the client) as a 499-style
  outcome at `INFO`/`WARN`, not as an `internal error`.
- flagd outages: log one `WARN` when flagd becomes unavailable and one `INFO` when it recovers,
  with a count of fallbacks in between; keep per-request detail at `DEBUG`. Today every request
  logs the same warning while flagd is down.

### Phase 3: upstream visibility

- `HTTPStore` logs each upstream call at `DEBUG` (method, path, status, duration), and failures at
  `WARN` with `error`.
- Upstream errors carry `upstream_status` and `upstream_duration_ms`, which the handler's
  `internal error` line includes, so one line is enough to debug an upstream failure.

### Phase 4: safety and tests

- Write down the redaction rules: never log bodies, query strings, or headers outside an
  allowlist; URLs always via `Redacted()`.
- Extend log-output tests so required keys, `request_id` on error lines, and levels can't drift.

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
- Feature flags with a flagd (OpenFeature) sidecar, evaluated over OFREP. The demo flag
  `items-list-total` adds `"total"` to `GET /items`.
- Flag fallbacks: when flagd can't answer, the API uses each flag's `defaultVariant` from
  `flagd/flags.json`, which is embedded in the binary. Flags are declared with `flags.Declare`,
  and the service won't start if a declared flag isn't defined. `FLAGS_FALLBACK_FILE` overrides
  the embedded definitions.
- `FLAGS_URL`, `FLAGS_TIMEOUT` and `FLAGS_FALLBACK_FILE` environment variables.

### Changed

- Log field renames, which affect any existing log queries:
  - `err` is now `error` (on `internal error` lines).
  - `duration` (integer nanoseconds in JSON) is now `duration_ms` (decimal milliseconds).
  - Timeouts on startup and shutdown lines are strings such as `5s` instead of nanoseconds.
- Feature-flag warnings are logged with the request context, so they carry `request_id`.
