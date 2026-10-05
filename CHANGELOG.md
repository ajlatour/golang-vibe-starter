# Changelog

Notable changes to this project. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). There are no tagged releases yet, so
everything is under Unreleased.

Ideas considered but not planned are listed under
[Open ideas](docs/development.md#open-ideas).

## Unreleased

### Added

- `.env.example`, listing every environment variable the API reads with its default, all commented
  out. Copy it to `.env` and uncomment lines to change settings under docker compose, which now
  reads `.env` if there is one (the integration stack ignores it). A test keeps the code,
  `.env.example` and the README's Configuration table in step, defaults included.
- The header guard's limits are configurable: `HEADER_MAX_COUNT`, `HEADER_MAX_BYTES` and
  `HEADER_MAX_TOTAL_BYTES`, defaulting to the values in `headerguard.DefaultPolicy` (100, 8192 and
  32768). Each must be a whole number from 1 to 1048576, with the per-header limit no larger than
  the total, or the service refuses to start, so a typo can't switch a limit off. The service logs
  the policy in force at startup (`using header guard`). `HEADER_GUARD_MODE` is now set explicitly
  in `compose.yaml`, and can be changed from `.env` or the shell.

- Request header screening (`internal/headerguard`), on every request before the handlers run: a
  request with a body must send `Content-Type: application/json` (415), headers are limited to 100,
  8 KiB each and 32 KiB in total (431), `Content-Type`, `Authorization` and
  `Client-Correlation-Id` may appear only once (400), and proxy and method-override headers
  (`X-Forwarded-*`, `Forwarded`, `X-Real-IP`, `X-HTTP-Method-Override` and similar) are removed.
  Rejections get a JSON error, a correlation ID and a `request headers rejected` log line.
  `HEADER_GUARD_MODE=report` logs instead of rejecting, for rolling the rules out. The OpenAPI spec
  documents the new statuses on every operation.

- `make docs-check` builds the documentation website and checks that every page exists and that
  every link and section reference in it resolves (`docs/site/site_test.go`, skipped by a plain
  `go test ./...`). CI runs it as a third job. `make docs` now starts from an empty `bin/docs`, so
  a page the build failed to produce can't survive from an earlier run and hide the failure.

- Local documentation: `make docs` renders the README, the guides in `docs/`, the changelog and
  the API spec into a small website in `bin/docs`, with navigation and working links between
  pages, and `make docs-open` opens it. pandoc and Redocly run in Docker, so there is nothing to
  install. `make godoc` serves the Go package documentation with pkgsite (pinned to v0.5.0) at
  http://localhost:6060.

- A `Makefile` with shortcuts for the commands that were long or easy to forget: running the
  stack (`make up`, `make run`, `make fault FAULT=...`), the checks and tests (`make check`,
  `make integration`, `make live-test`) and the API spec (`make spec`, `make spec-lint`, which run Redocly CLI in Docker, so there is
  nothing extra to install). `make
  help` lists them. It works with GNU Make 3.81, the version macOS ships. CI now runs these same
  targets instead of its own copies of the commands.

- Continuous integration (`.github/workflows/ci.yml`), on pushes to `main` and on pull
  requests: a fast job for `gofmt`, `go vet` and `go test -race ./...`, and a job that starts the
  stack with docker compose and runs the Godog suite against it, printing the containers' logs on
  failure.
- Integration tests, phase 3: correlation ID scenarios. A valid `Client-Correlation-Id` is echoed
  back (including at the 128-character limit), a missing or invalid one (spaces, a slash,
  non-ASCII, a quote, 129 characters) is replaced by a generated one, generated IDs differ per
  request, and 404, 422 and 502 responses carry the ID too. The header's name and format are read
  from `docs/openapi.json`.
- Integration tests, phase 2: scenarios for every WireMock fault (502 for an unavailable
  upstream, a reset connection and truncated JSON, 504 for a timeout, 200 for a slow answer within
  the timeout) and for the `items-list-total` flag. Flags are switched by editing the tests' own
  copy of the definitions, `integration/flagd/flags.json`, which the compose override mounts into
  flagd; the suite keeps it in step with `flagd/flags.json`. Test code never calls Docker.
- Integration tests, phase 1: Gherkin scenarios run with Godog (v0.16.0) against the whole stack
  (API container, WireMock, flagd), in `integration/`, its own Go module so the service keeps no
  third-party dependencies. 21 scenarios cover health, listing and reading items, create, replace
  and delete, unknown IDs, and request validation (400 and 422). `compose.integration.yaml` sets a
  3s upstream timeout and JSON logs for testing. The suite checks the stack is up first and resets
  WireMock before each scenario.

- An OpenAPI 3.0 spec of the API, `docs/openapi.json`: the routes, request and response schemas,
  status codes (including 502 and 504 for upstream failures) and the `Client-Correlation-Id`
  header. Tests (`cmd/api/openapi_test.go`) run the real handlers against it, so the spec and the
  code can't drift apart: routes, statuses, bodies, headers, the `Item` fields, the name-length
  limit and the correlation ID format are all checked.

- Request and upstream lifecycle logging: a log line at each of the four ends of a request, all at
  `INFO` and sharing the request's `request_id`:
  - `request received` when it arrives (`method`, `path`, `content_length`);
  - `upstream request sent` before each upstream call (`upstream`);
  - `upstream response received` when the upstream answers, whatever the status (`upstream`,
    `upstream_status`, `upstream_duration_ms`);
  - `response sent` when the reply goes out (`method`, `route`, `path`, `status`, `bytes`,
    `duration_ms`). This is the former `request` line, renamed.

  `/healthz` stays at `DEBUG`. The log contract test checks that every request opens with
  `request received`, closes with `response sent`, and that every upstream request sent has
  exactly one outcome. See the Logging section of the README.

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

- The item code is split along domain-driven lines; the API is unchanged. `internal/item` is now
  the pure domain (a test keeps HTTP, JSON and logging out of it), with an `item.Name` value
  object that carries the name rules (trimmed, required, at most 200 characters). The handlers
  and the API's JSON types moved to `internal/item/httpapi`, and `httpstore` maps the upstream's
  JSON to domain items through its own wire types. Every Store now refuses an item without a name
  (`item.CheckStorable`), and upstream data that breaks a domain rule is reported as a 502
  instead of being passed on.
- A request with a body sent as anything but `application/json`, or with no `Content-Type`, is now
  refused with 415; before, any body was parsed as JSON whatever its declared type. Clients that
  omit `Content-Type` need to send it.
- Package structure, with no change in behavior:
  - `internal/flags` is renamed `internal/featureflag`, so it no longer reads like the standard
    library's `flag` (command-line flags), and so `flags` stops being shadowed by a variable of
    the same name in the item handlers.
  - The environment reader and the settings every service shares move from `internal/httpx` to
    a new `internal/envconfig` (`httpx.ServerConfig` is now `envconfig.Service`), leaving `httpx`
    purely about HTTP. `httpx.Serve` takes the listen address and shutdown timeout directly.
  - The upstream client moves out of `internal/item` into `internal/item/httpstore`
    (`item.NewHTTPStore` is now `httpstore.New`, `item.UpstreamError` is
    `httpstore.UpstreamError`), so `item` holds just the model, the `Store` interface and the
    handlers. The store contract every implementation must pass is now `itemtest.StoreContract`.
- Documentation is reorganised. The README is now a short guide (what the service is,
  prerequisites, running, testing, the API, configuration and layout), and the detail moved to
  `docs/`: `local-stack.md` (WireMock, faults, load testing), `feature-flags.md`, `logging.md`
  (the field and message tables, which the schema tests now check there) and `development.md`
  (what to update when you change something, the tests, CI, and the open ideas that used to be the
  Roadmap here).
- Upstream failures now return `502 Bad Gateway`, or `504 Gateway Timeout` when the upstream timed
  out, instead of `500 Internal Server Error`. A 500 said the API itself had broken. The 502 covers
  connection refused or reset, an upstream error status, and a body that won't decode. The response
  body is the generic `{"error":"bad gateway"}` or `{"error":"gateway timeout"}`; the upstream's
  message stays in the logs. `500` is now only for failures in the API itself. Clients and
  alerting that match on 500 for upstream outages need updating.

- Log message renames, which affect any existing log queries: `request` is now `response sent`,
  and `upstream request` (a successful call, formerly `DEBUG`) is now `upstream response received`
  at `INFO`. A request now writes four `INFO` lines where it wrote one, plus one `upstream request
  sent` per upstream call.

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

### Fixed

- The integration tests could leave flagd serving a stale flag value for the rest of the run.
  They replaced the flags file by writing a temporary file and renaming it, and flagd, which
  watches the file, sometimes failed to re-attach its watch to the new file ("error restoring
  watcher") and never reloaded again. The tests now edit the file in place. The note in
  `compose.yaml` that replacing the file is safe is corrected.
- The README suggested `npx @redocly/cli preview-docs` to view the API spec, a command Redocly CLI
  2 removed. `make spec` renders the spec with `build-docs` instead.
- The API spec now states that the API has no authentication (`"security": []`), which Redocly's
  linter flagged as missing.
