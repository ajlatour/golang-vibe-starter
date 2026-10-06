# Changelog

Notable changes to this project. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions on
[Semantic Versioning](https://semver.org/). Changes not yet released are under Unreleased; each
release has a section of its own, made by `make release-prepare` ([Releasing](docs/releasing.md)).

Ideas considered but not planned are listed under
[Open ideas](docs/development.md#open-ideas).

## Unreleased

### Added

- CI's own time is tracked: after every successful CI run on `main`, a new workflow
  (`ci-times.yml`) saves the pipeline's time, each job's time and wait for a runner, and each
  step's time to the `perf-data` branch, and `make perf-trends` charts the pipeline and its jobs
  beside the performance trends ([Pipeline time](docs/performance.md#pipeline-time)).
- Every release attaches its documentation: the docs site as of the tag, with a new Performance
  history page charting `main`'s performance and CI time up to the release, and the records it
  charts ([Releasing](docs/releasing.md#what-the-tag-publishes)). `make docs-release` builds it
  locally; `make docs` shows the page as a note, so it needs no records.

### Changed

- The OpenAPI spec's `info.version` is the latest release's: `make release-prepare` sets it with
  the changelog, a test fails if the two disagree, and the Release workflow refuses a tag that
  doesn't match it. It said 0.1.0 through the 0.2.0 release, and now says 0.2.0.

## [0.2.0] - 2026-10-05

### Added

- `FLAGS_CACHE_TTL` (1s): the API keeps each flag value flagd serves for that long, so it asks flagd
  about a flag about once a second however busy it is. While flagd isn't answering, flagd is retried
  once per TTL and every evaluation in between takes its default at once, where each used to wait
  out `FLAGS_TIMEOUT` (500ms). `0s` asks flagd every time, as before; the integration tests do.

### Changed

- The upstream and flagd clients each keep their own connection pool, with as many idle
  connections as `MAX_IN_FLIGHT` (100 with no cap), where Go's default kept 2: under load, calls
  reuse connections instead of opening new ones. They also no longer route through a proxy named
  in `HTTP_PROXY`, `HTTPS_PROXY` or `NO_PROXY`.
- An item's `description` is at most 2000 characters; a longer one is a 422. It was limited only
  by the 1 MiB body.
- The documentation site's navigation, and the README's Documentation section, group the pages
  by theme: Start, Use the API, Run it, Build and ship, and Architecture. A guide in `docs/`
  that isn't in a theme fails the site build, and a test keeps the README's groups matching
  the site's. Long inline code wraps on a phone instead of widening the page.

### Fixed

- `http_requests_in_flight` no longer drifts upward if a handler aborts with
  `http.ErrAbortHandler`: such a request is now counted as finished too.

## [0.1.0] - 2026-10-05

### Security

- API keys (`internal/apikey`): every route but `/healthz` needs one, sent as
  `Authorization: Bearer <key>`; 401 without a valid key, 403 for a write with a read-only one.
  `API_KEYS` lists the keys as `name:scope:sha256`, so only hashes are configured, and every
  configured hash is compared in constant time. `make api-key` generates a key and its entry.
  The service refuses to start with no keys unless `API_AUTH=off` (local development only,
  logged as a warning), and `API_AUTH_READS=open` lets reads through without a key. The compose
  stack sets two documented development keys, which `scripts/api`, k6 and the integration tests
  use; `scripts/api` sends one only to the local stack.
- Limits (`internal/limit`): at most `MAX_IN_FLIGHT` requests (512) are served at once, and more
  get 503, before authentication, so floods without a valid key are capped too; and each client,
  its API key or else its IP address, may make `RATE_LIMIT` requests a second (100) in bursts of
  `RATE_BURST` (200), and more get 429. Both set `Retry-After`, and `/healthz` is exempt. The
  compose stack turns the per-client limit off, so load tests measure the API.
- Item IDs are checked before they reach the upstream: an ID must be 1 to 128 letters, digits,
  `-` and `_` (`item.ValidID`), and any other is a 404 that never leaves the API. Before, an ID
  such as `%2E%2E` reached the upstream as `/items/..`, which an upstream or proxy that
  normalizes paths or decodes `%2F` could route somewhere other than `/items/{id}`. The rule is
  in `docs/openapi.json`, and `httpstore` refuses such IDs too.
- The compose stack publishes the API (8080), WireMock (8081) and flagd (8016) on `127.0.0.1` only,
  like the ports added since. Before, they listened on every interface, so anyone on the same
  network could use WireMock's unauthenticated admin API to change what the API was served.
- Calls to the upstream and to flagd never follow redirects (`jsoncall`): a 3xx is a failed call,
  so the API answers 502. Before, a compromised or misconfigured upstream could redirect the API to
  any address, internal ones included, and on a 307 or 308 the request body went along.
- The profiling listener (`PPROF_ADDR`) logs a WARN at startup when other machines can reach it,
  and a CPU profile or trace is limited to 60 seconds, since pprof itself sets no limit on how
  long a caller can keep the CPU busy.
- Error messages for an unusable body describe it in JSON's terms (`field "name" must be a
  string`); before, a type mismatch quoted encoding/json, naming the service's Go types.
- Every response carries `X-Content-Type-Options: nosniff` and `Cache-Control: no-store`.
- The server reads at most twice `HEADER_MAX_TOTAL_BYTES` of request line and headers (64 KiB by
  default) before refusing a request with a plain 431 of its own; before, Go's default let every
  connection send 1 MiB before the header guard saw it.
- The API container in compose runs with a read-only filesystem, no Linux capabilities and
  `no-new-privileges`.
- CI's supply chain: every action is pinned to a commit SHA and every container image to a
  digest, Dependabot proposes weekly updates for Go modules, actions and images, and a new `vuln`
  job (`make vuln`) runs govulncheck over all three modules. The performance workflow's comment is
  posted by a job that never runs the pull request's code; the job that builds the report tool
  from it is read-only.

### Added

- API keys can be rotated without a restart: `API_KEYS_FILE` names a file of keys, one per line,
  checked every `API_KEYS_RELOAD` (10s) and swapped in once a change reads the same on two checks,
  so a half-written file is never used (`API keys reloaded`). A
  file that can't be read or parsed, or holds no keys, changes nothing (`API keys not reloaded`),
  so a bad edit can't lock clients out. A key may carry its own rate limit, a fourth field
  (`ci:write:<hash>:500/1000`), which replaces `RATE_LIMIT` for it.
- A release process ([Releasing](docs/releasing.md)): `make release-prepare` cuts the changelog
  section in a pull request, and `make release-tag` tags `main`. The tag publishes the image to the
  GitHub Container Registry for linux/amd64 and linux/arm64, with an SBOM and build provenance,
  and a GitHub release with the changelog section as its notes. Only a tag on `main` publishes;
  the workflow can also be run by hand as a dry run. The Dockerfile cross-compiles, so the second
  platform needs no emulation.
- Static analysis: `make lint` and a CI job run staticcheck and gosec on all three modules, at
  pinned versions. Their first run found a test condition that only worked by accident, an
  unchecked hex decode in `apikey.ParseKeys`, and five unhandled `Close` errors, all fixed. (CodeQL
  would need GitHub Advanced Security on this private repository; ADR-0026.)
- Architecture documentation. [Architecture](docs/architecture.md) covers the context and scope,
  the request path, the packages and their dependency rules, runtime behaviour, deployment, and 36
  requirements, each naming what verifies it. [Architecture decisions](docs/decisions.md) has 25
  records of why it's built that way. `docs/architecture_test.go` holds both to the code: the
  service's `go.mod` requires nothing, packages import only what the table allows, every path
  named exists, and every record is complete and indexed.
- Performance over time ([trends](docs/performance.md#trends)): after every merge, a new workflow
  measures `main` (benchmarks and the `compare` load profile) and saves one JSON record per commit
  on the `perf-data` branch. `make perf-trends` charts them as a standalone HTML page, drawn by
  the report tool (`report record`, `report trends`) with no chart library.
- Fuzz tests for the code that handles untrusted input: JSON request bodies, correlation IDs, the
  header guard, item IDs, `API_KEYS` and the request metrics' labels, each checking a property
  that must hold for any input. `make fuzz` runs them, and a new CI job runs each for 10 seconds.
- Every log line of a request made with an API key names the key (`api_key`, never the key
  itself), `response sent` included, so the logs say which client did what.
- Request metrics on `/metrics` (`internal/httpmetrics`, standard library only):
  `http_requests_total` and the `http_request_duration_seconds` histogram by route pattern, method
  and status, and `http_requests_in_flight`. Labels are bounded (the matched pattern, `none`
  before routing, `OTHER` for unusual methods), and recording a request allocates nothing. The
  Grafana dashboard has a row for them.
- `/readyz`, a readiness probe beside `/healthz`: 200 while the instance can reach the upstream (any
  answer counts, an error included), 503 once it starts shutting down. Checks are cached for a
  second, and changes are logged once each way (`not ready`, `ready again`). `SHUTDOWN_DELAY`
  keeps serving for a while after SIGTERM, reporting not ready, so a load balancer can stop
  sending requests before the server stops accepting them.
- Resource monitoring ([watching resources](docs/performance.md#watching-resources)):
  - The API serves its Go runtime metrics, and the process's CPU time and memory, at `/metrics`
    on `PPROF_ADDR`, in Prometheus's text format, with the standard library alone.
  - Every `make perf` run samples each container's CPU and memory with `docker stats` and reads
    the API's metrics before and after, and prints what the run cost. The pull request report
    compares the API's CPU per request, peak memory, and allocations and GC cycles per request,
    base against PR, and warns on a large increase.
  - `make monitor` starts cAdvisor, Prometheus and Grafana (the `monitoring` compose profile,
    published on loopback only) with a dashboard of k6's load, every container's CPU, memory and
    network, and the API's runtime, on one timeline; k6 sends its results there while it's up.
    `make down` stops the profiles too.
- Performance testing ([Performance](docs/performance.md)):
  - Go benchmarks for the request path (`BenchmarkAPI` through the whole service, and the
    middleware, header guard and logger on their own). `make bench` runs them;
    `make bench-compare` compares them with `main` using benchstat.
  - k6 load profiles in `perf/profiles.json`, run in Docker on the stack's network:
    `make perf PROFILE=smoke|load|stress|spike|soak`. Fixed-rate traffic, per-phase
    thresholds, and a JSON summary in `bin/perf/`.
  - `PPROF_ADDR` serves the runtime profiles (pprof) on a listener of its own; off by default.
    The compose stack sets it, published on `127.0.0.1:6061`. `make perf-profile` captures CPU,
    heap, allocation and goroutine profiles during and after a load run.
  - Every pull request gets a performance report from the new Performance workflow, kept in one
    comment edited on each push: the benchmarks and the k6 smoke profile, run against the pull
    request and its base interleaved on one runner, compared with benchstat's statistics
    (`perf/report`). It fails only on more allocations per operation (unless the pull request has
    the `perf-ok` label) or crossed load thresholds; timing changes are reported, never failed
    on. `make perf-compare` writes the same report locally. The benchmark and load comparisons run
    as parallel jobs, with many short benchmark samples (300ms, 10 rounds) and a dedicated 20s
    `compare` k6 profile, and the workflow skips changes to Markdown and `docs/` alone.
- `scripts/api`, for calling the API by hand: `scripts/api POST /items '{"name": "Sprocket"}'`.
  It wraps curl, adding `Content-Type: application/json` to a body (from the argument, a file or
  stdin) and an optional `CORRELATION_ID`, prints the status and correlation ID, pretty-prints
  the body with jq when jq is installed and the output is a terminal, and exits 1 on a 4xx or
  5xx. `API_URL` points it elsewhere. Needs only bash (3.2 or later) and curl.
- [Calling the API](docs/curl.md): an example of every route, error and header rule, with
  `scripts/api` and with plain curl. A test runs every command on the page against the real
  handlers and checks the status it promises, and that every route is called successfully.
- `itemtest.MemStore.Seed`, to start a test from items with known IDs.
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

- CI keeps Go's build cache between runs for the Docker image builds (`scripts/build-api-image.sh`,
  with `actions/cache`), so the integration job's build and the load comparison's two builds
  recompile only what changed instead of everything. The Dockerfile takes the cache as an
  optional build context; a plain `docker build` or compose is unchanged.
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

- The pull request load comparison's base build failed once `main` had a kept Go build cache: the
  pull request's build replaced the cache directory while the base's, running alongside, could
  still be reading it. Both images now build before the cache is kept, a failed copy of the cache
  no longer fails a build, and a failed build prints its output instead of hiding it.
- The `route` field of every "response sent" line was empty since authentication was added:
  authentication hands the router a copy of the request, and the route it matched never reached
  the logging middleware outside. `httpx.RecordRoute` now records it in the request's shared log
  state, at no extra allocation.
- Labelling a pull request (Dependabot labels its own as it opens them) no longer cancels its
  performance comparison: only the `perf-ok` label shares the comparison's concurrency group;
  other label changes get their own, and skip.
- The pull request load comparison measured the pull request against itself: `docker compose run`
  restarted the API on compose's default image before every k6 run, replacing the base's image.
  k6 now runs with `--no-deps`, and `perf/load-ab.sh` checks after every run that the API ran the
  image it chose.
- The integration tests could leave flagd serving a stale flag value for the rest of the run.
  They replaced the flags file by writing a temporary file and renaming it, and flagd, which
  watches the file, sometimes failed to re-attach its watch to the new file ("error restoring
  watcher") and never reloaded again. The tests now edit the file in place. The note in
  `compose.yaml` that replacing the file is safe is corrected.
- The README suggested `npx @redocly/cli preview-docs` to view the API spec, a command Redocly CLI
  2 removed. `make spec` renders the spec with `build-docs` instead.
- The API spec now states that the API has no authentication (`"security": []`), which Redocly's
  linter flagged as missing.
