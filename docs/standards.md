# Engineering standards

How code is written, reviewed and shipped in this repository, and in services started from it. A
rule says **must** when a check enforces it and the build fails without it, and **should** when
review does. Each says why, and what checks it. Where another guide covers a topic in depth, this
links to it; [Development](development.md#making-a-change) lists what else to update when you
change something.

- [Principles](#principles)
- [Layout](#layout)
- [Go code](#go-code)
- [HTTP APIs](#http-apis)
- [Logs, metrics and traces](#logs-metrics-and-traces)
- [Security](#security)
- [Testing](#testing)
- [Documentation](#documentation)
- [Changes, reviews and releases](#changes-reviews-and-releases)
- [Dependencies and tools](#dependencies-and-tools)

## Principles

1. **The standard library first.** The service has no third-party dependencies
   ([ADR-0002](adr/0002-the-service-uses-the-standard-library-only.md)). A dependency is a
   decision, recorded in an ADR, not a convenience.
2. **What can be checked is checked.** A rule a test or a linter can enforce is enforced, so
   review is for what only people can judge. Every rule below names its check.
3. **Documentation changes with the code.** Descriptions of the code (the API spec, the log
   tables, the settings) have tests that fail when the two disagree.
4. **Fail loudly at startup, never quietly at runtime.** Bad configuration stops the service
   with every problem listed; a missing flag definition stops it; nothing falls back silently.
5. **Everything is bounded.** Every request has a deadline, a body limit and a place in a queue
   of limited length; every call out has a timeout; every map keyed by clients has a cap.
6. **Every request is answered, and logged as what the client got.** Errors are JSON with a
   code, whatever went wrong ([ADR-0043](adr/0043-every-request-the-handler-gets-is-logged-as-what-the-client-got.md)).
7. **Small changes, each green before it merges.** One concern per pull request; CI is the
   gate, and a red check is never merged past.

## Layout

| What | Where | For example |
| ---- | ----- | ----------- |
| A binary | `cmd/<name>/`: configuration, wiring and nothing else | `cmd/api`, `cmd/apikey` |
| A feature: a resource the API serves | `internal/<feature>/`: its model, its store interface, its handlers | `internal/item` |
| An adapter to something outside | `internal/<feature>/<adapter>/` | `internal/item/httpstore` |
| A package's test helpers | `internal/<package>/<package>test/`, like `net/http/httptest` | `internal/item/itemtest` |
| A protection (authentication, limits, screening) | A package of its own, as middleware | `internal/apikey`, `internal/limit`, `internal/headerguard` |
| Shared HTTP, logging and configuration plumbing | `internal/httpx`, `internal/logx`, `internal/envconfig` | |
| Code with third-party dependencies: tests or tools | A Go module of its own | `integration/`, `perf/report/` |
| Scripts | `scripts/`, POSIX `sh` | `scripts/changelog.sh` |
| Decisions | `docs/adr/`, a file each | |
| Release notes in waiting | `changes/`, a file each | |

- Everything **must** be under `internal/`, except binaries: nothing here is a library for other
  modules until that's decided. *Checked by:* review.
- A package **must** import only what the package table in [Architecture](architecture.md#building-blocks)
  allows it. *Checked by:* `docs/architecture_test.go`.
- A package is named for what it provides, never `util`, `common` or `helpers`, and does one
  thing. Its doc comment **must** say what it's for and why it exists. *Checked by:* staticcheck
  (ST1000), review.

## Go code

### Style

- Code **must** be `gofmt`ed, and pass `go vet`, staticcheck with every check (style included)
  and modernize. *Checked by:* `make fmt-check`, `make vet`, `make lint`.
- Names follow [Effective Go](https://go.dev/doc/effective_go#names) and the
  [Code Review Comments](https://go.dev/wiki/CodeReviewComments): `MixedCaps`; short receivers;
  short, lower-case package names that don't stutter (`item.Store`, not `item.ItemStore`);
  `ErrX` for sentinel errors and `XError` for error types; `New` or `NewX` for constructors;
  `WithX` for options; a unit in a name when the type doesn't carry one (`timeout
  time.Duration`, but `timeoutMS int`).
- Every package and every exported name **must** have a doc comment: complete sentences,
  starting with the name. *Checked by:* staticcheck (ST1000, ST1020 to ST1022).
- Comments say *why*. What the code does should be plain from the code; when it isn't, the code
  should change first.
- A function should fit on a screen. One that needs a comment per block wants splitting, as
  `run` in `cmd/api/main.go` was.

### Errors

- Return errors; don't panic. A panic is for a programming mistake: a `MustX` helper in tests,
  an impossible state.
- Wrap an error with what was being done, and `%w`: `fmt.Errorf("upstream %s: %w", call, err)`.
  Messages are lower case, without a full stop. *Checked by:* staticcheck (ST1005).
- A condition callers branch on is a sentinel (`var ErrNotFound = errors.New(...)`); one that
  carries data is a type (`*httpstore.UpstreamError`). Check them with `errors.Is` and
  `errors.AsType`, never by message.
- Handle an error once: return it, or log it, not both.
- The HTTP edge decides what a client is told. Handlers return errors (`httpx.HandlerFunc`) and
  `httpx.Handle` answers them: a client's mistake as an `*httpx.Error` with a code from the
  catalog, anything else as a logged 5xx with a generic message. Internal detail **must not**
  reach a response. *Checked by:* `cmd/api/openapi_test.go`, `internal/httpx/fuzz_test.go`.
- An error that should add fields to the line that logs it implements `logx.AttrsError`.

### Context

- `ctx context.Context` is the first parameter of anything that does I/O or may block, and is
  never stored in a struct.
- Handlers use `r.Context()`. It carries the request's deadline, its ID and trace, and the
  client's cancellation.
- `context.Background()` is for the top of a program and for work that outlives every request.
  A line logged that way **must** be listed in `logtest.OutsideRequests`. *Checked by:*
  `internal/logx/schema_test.go`.
- Honour cancellation: every call out takes the context, and every loop that waits selects on
  `ctx.Done()`.

### Concurrency

- Every goroutine has an owner and a way to stop: its context. Background tasks start through
  `logx.Go`, which recovers a panic so one task's bug doesn't end the process
  ([ADR-0044](adr/0044-a-panic-or-memory-pressure-doesnt-take-the-process-down-with-every-request.md)).
  Service code doesn't use a bare `go`. *Checked by:* review.
- Shared state is small, and guarded by a mutex or atomics. Every test runs with the race
  detector. *Checked by:* `make cover`, which runs `-race`.
- Nothing grows without a bound: not a queue, not a map keyed by clients (`limit.MaxClients`),
  not goroutines per request.

### Construction and dependencies

- A component is given what it uses: constructors take the logger, the client, the store. There
  is no package-level mutable state, no `init`, and nothing outside `cmd/` reads the
  environment. *Checked by:* review.
- Optional settings are functional options: `httpstore.WithRetries(1)`.
- An interface belongs to the package that uses it, and is small: `item.Store`, `item.Flags`,
  `validator.Checker`. Assert an implementation at compile time: `var _ item.Store =
  (*Store)(nil)`.
- Every dependency the service calls gets an `*http.Client` of its own, with a timeout and its
  own connection pool (`newTransport` in `cmd/api`), never `http.DefaultClient`.

### Configuration

- Settings are environment variables, read in `cmd/<binary>/config.go` through
  `internal/envconfig`. Each has a default in code, or is required. An invalid value, or
  settings that can't work together, **must** stop startup, with every problem reported at
  once ([ADR-0010](adr/0010-configuration-is-environment-variables-documented-and-checked.md)).
  *Checked by:* `cmd/api/config_test.go`.
- Every setting **must** be in `.env.example` and the README's table, with its default.
  *Checked by:* `cmd/api/envexample_test.go`.
- A configuration a deployment runs with **must** keep starting, or the release that breaks it
  says so. *Checked by:* `cmd/api/configs_test.go`, with `cmd/api/testdata/configs`.
- Only hashes of secrets are configured (API keys), and no secret is ever logged. *Checked by:*
  `cmd/api/logcontract_test.go`.

### Performance

- Measure before optimizing: the benchmarks (`make bench`), the load tests and the profiles
  ([Performance](performance.md)).
- A pull request **must not** add allocations on a benchmarked request path unless it means to;
  then it carries the `perf-ok` label and says why. *Checked by:* the Performance workflow.
- Tricks that save an allocation (a response made once, a canonical header key, a stack buffer)
  are for code a benchmark shows is hot, with a comment saying why.

## HTTP APIs

- `docs/openapi.json` is the contract, and changes in the same pull request as the handlers.
  *Checked by:* `cmd/api/openapi_test.go`, which runs every documented status of every
  operation against the real handlers, and `cmd/api/openapi_routes_test.go`.
- **Paths** are plural nouns: `/items`, `/items/{id}`. No verbs. An ID is opaque to clients and
  checked at the edge: one that can't exist is a 404 that never reaches the store
  ([ADR-0017](adr/0017-item-ids-are-checked-against-an-allowlist.md)).
- **Methods:** `GET` reads; `POST` creates, answering 201 with `Location` and the new resource;
  `PUT` replaces, answering 200 and the resource; `DELETE` removes, answering 204.
- **Statuses:**

  | Status | When |
  | ------ | ---- |
  | 400 | The request is malformed: its body, a parameter, a header used twice, its target |
  | 401, 403 | No valid key; a key that may not do this |
  | 404, 405 | No such resource or route; a method its path doesn't have (with `Allow`) |
  | 412 | `If-Match` no longer matches |
  | 415, 422 | A body that isn't JSON; JSON that breaks a rule |
  | 429, 503 | Over the client's rate, or the service's capacity, with `Retry-After` |
  | 431 | Headers over a limit |
  | 500 | A failure of this service's own |
  | 502, 504 | A dependency failed, or was too slow |

- **JSON** fields are `snake_case`; times are RFC 3339, in UTC; an optional field without a value
  is left out. A request body must be `application/json`, at most 1 MiB, with no unknown fields.
- **Errors** are always `{"error": "...", "code": "..."}`: `code` for programs to act on,
  stable; `error` for people, and free to change. Codes come from one list,
  `internal/httpx/codes.go`, which the spec and the README repeat. *Checked by:*
  `cmd/api/openapi_codes_test.go`.
- **Lists** are always paged: `limit` (50 by default, at most 200) and an opaque `next_cursor`.
  No endpoint returns an unbounded list
  ([ADR-0037](adr/0037-the-list-is-paged-by-cursor-50-a-page-by-default.md)).
- **Single resources** carry an `ETag`. Writes honour `If-Match` (412 when it doesn't match),
  reads `If-None-Match` (304)
  ([ADR-0040](adr/0040-items-have-etags-and-writes-can-be-conditional.md)).
- **Versions** are chosen by the `API-Version` header, never the path
  ([ADR-0036](adr/0036-the-api-is-versioned-by-a-header-not-by-path.md)). A change that may break
  clients **must** have a `**Breaking**` entry in `changes/`, and the release a big enough
  version bump. *Checked by:* `make api-compat` (`cmd/apidiff`), in CI and the release.
- **Every route** but the probes is behind authentication, the rate limits and request
  validation, in a fixed order. *Checked by:* `cmd/api/middleware_order_test.go`.
- **Time:** every request has a deadline (`REQUEST_TIMEOUT`); every call out, a timeout shorter
  than it. Only a fast failure of an idempotent call is retried, within a budget
  ([ADR-0041](adr/0041-every-request-has-a-deadline-inside-the-servers-write-timeout.md)).
  *Checked by:* `cmd/api/config_test.go`, `cmd/api/deadline_test.go`.
- **A new route** touches the spec, the handlers, `docs/curl.md`, the WireMock stubs, the load
  test's traffic mix and an integration scenario. The tests name whichever is missing.

## Logs, metrics and traces

[Logging](logging.md#rules-for-log-lines) has the rules in full. In short:

- Logs are structured (`log/slog`, through `internal/logx`), JSON in production. Field names
  **must** be the constants in `internal/logx/keys.go`, and every message **must** be in the log
  contract, at its levels, with its fields. *Checked by:* `internal/logx/schema_test.go`,
  `cmd/api/logcontract_test.go`.
- Levels: `ERROR` needs a person; `WARN` is degraded but handled; `INFO` is the lifecycle and each
  request's two ends; `DEBUG` is detail.
- Never logged: bodies, headers, query strings, keys, or anything else a client sent that the
  log doesn't need. Values from outside are bounded in length.
- Each request's lines carry its `request_id` and `trace_id`; every call out carries the trace on
  (W3C `traceparent`).
- Metrics follow Prometheus's conventions (`_total` for counters, the unit in the name), and no
  label takes a value a client chooses. *Checked by:* `internal/httpmetrics` tests.

## Security

- Least privilege by default: authentication is on, keys are scoped to reading or writing, and
  only the probes are exempt
  ([ADR-0016](adr/0016-clients-authenticate-with-hashed-api-keys.md)).
- Untrusted input is parsed at the edge into types that can only hold valid values (`item.ID`,
  `item.Name`), bounded in size, and fuzzed. *Checked by:* the fuzz tests, in CI's fuzz job.
- A static analysis finding is fixed, or accepted on its line with the reason. *Checked by:*
  `make lint` (gosec among it).
- The supply chain is pinned: the service has no dependencies, tools run at fixed versions or
  image digests, actions at commit SHAs, and Dependabot proposes updates weekly. Known
  vulnerabilities fail the build. *Checked by:* `docs/pins_test.go`, `make vuln`.
- The image is distroless and runs as a non-root user; the local stack listens on loopback only.
- Vulnerabilities are reported privately: [`SECURITY.md`](../SECURITY.md).

## Testing

- Unit tests **must not** start or call Docker, and stay fast: the whole suite runs in seconds.
  The integration tests talk HTTP to a stack started separately
  ([ADR-0009](adr/0009-integration-tests-run-gherkin-against-the-composed-stack.md)).
- Test behaviour through the public surface: the handlers through `newHandler` and real requests,
  a store through `itemtest.StoreContract`, the API through its spec.
- Name a test for the behaviour it holds (`TestFullLimiterSharesOneLimit`). Use tables where cases
  differ only in data, with subtest names that read as sentences.
- A new check is proven: break what it guards, and see it fail. The pull requests here record
  these mutation checks.
- Code that parses untrusted input has a fuzz test; an input that once failed stays as a seed.
- Coverage **must** stay above the floor (`COVER_MIN`, 90%). New code comes with its tests, and
  raising the floor is welcome. *Checked by:* `make cover`, in CI's test job.
- Wait on a channel, or poll with a deadline (`logtest.Buffer.WaitFor`), never `time.Sleep` and
  hope.
- A flaky test is a bug, fixed or reported, never skipped to get green.

## Documentation

- **What goes where:** the README says what the service is and how to run it; `docs/` has a
  guide per theme; `docs/adr/` has the decisions; `changes/` has the next release's notes, which
  the release turns into `CHANGELOG.md`; `ROADMAP.md` has what's proposed.
- Write for the reader who wasn't there: what, then why, in plain words, matching the voice of
  the guides around it. Examples should run: `docs/curl.md`'s do, in a test.
- Documentation changes in the pull request that changes what it describes. Preview it with
  `make docs-open`. *Checked by:* `make docs-check`, which builds the site and checks every link,
  and the drift tests in [Development](development.md#making-a-change).

## Changes, reviews and releases

- **One concern per pull request**, small enough to review in one sitting.
- **Commit messages:** a subject that says what the change does, in the imperative, in sentence
  case, without a full stop, in about 50 to 70 characters; then a body that says why, and what
  it trades off.
- **The pull request** says what changed and why, and how it was tested, including what was
  broken on purpose to prove a new check. The template asks for both.
- **CI must be green before a merge.** A test is never disabled or skipped to get there.
- **Alongside the change:** a file in `changes/` for anything users of a release should know
  ([how](../changes/README.md)); a record in `docs/adr/` for an architectural choice
  ([how](decisions.md)); a `**Breaking**` entry for a change that may break clients.
- **Releases** follow [Semantic Versioning](https://semver.org/): while the version is 0.x, a minor
  release may break clients. A release is cut with `make release-prepare` and `make
  release-tag`, and its image is smoke-tested before it's published ([Releasing](releasing.md)).

## Dependencies and tools

- The service **must** import the standard library only. A dependency needs an ADR that
  supersedes [ADR-0002](adr/0002-the-service-uses-the-standard-library-only.md) first. *Checked
  by:* `docs/architecture_test.go`.
- Code that needs a dependency to test or build the service lives in a module of its own
  (`integration/`, `perf/report/`).
- Tools run at pinned versions: Go tools through `go run tool@version` in the Makefile, the rest
  as Docker images pinned by digest in `tools/images/Dockerfile`. Nothing needs installing
  beyond Go, Docker and `make`.
- Every command a contributor runs is a `make` target, and CI runs the same targets
  ([ADR-0011](adr/0011-tooling-runs-in-docker-or-through-go-run.md)).
