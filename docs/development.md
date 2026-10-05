# Development

How the code is put together, how to change it safely, and how it's tested.

- [Ground rules](#ground-rules)
- [Making a change](#making-a-change)
- [Tests](#tests)
- [Integration tests](#integration-tests)
- [Continuous integration](#continuous-integration)
- [Open ideas](#open-ideas)

## Ground rules

- **The service uses the standard library only.** The root `go.mod` has no third-party
  dependencies. Where a tool would normally pull one in, the code does the job itself: flags over
  plain HTTP (OFREP) instead of an OpenFeature SDK, a JSON API spec instead of YAML.
- **Test-only dependencies live in their own module.** The integration tests need Godog, so they
  are a separate Go module in `integration/`, and the service never depends on it.
- **Documentation that describes the code is checked against it.** The API spec, the log tables,
  the flag definitions and the store contract each have tests that fail when the code and the
  description disagree. The next section lists them.
- **Tests never call Docker.** The integration tests talk HTTP to a stack you start yourself.
- **Commands live in the `Makefile`.** `make help` lists them, and CI runs the same targets, so
  what passes locally passes in CI.
- **Preview documentation before you push.** `make docs-open` renders the README, these guides,
  the changelog and the API spec as a local website (`docs/site/build.sh` does the rendering, with
  pandoc in Docker). `make docs-check` builds it and checks that every page exists and every
  link and section reference in it resolves; CI runs that too. `make godoc` serves the Go doc
  comments at http://localhost:6060.

## Making a change

When you change one of these, update its description in the same change. A test fails if you
don't, and its message says what to fix.

| If you change | Also update | Checked by |
| ------------- | ----------- | ---------- |
| A route, status code, request or response body, or header | `docs/openapi.json` | `cmd/api/openapi_test.go` runs the real handlers against the spec |
| The fields of `item.Item`, the name limit, or the correlation ID format | `docs/openapi.json` | `cmd/api/openapi_test.go` |
| A log message (new, renamed, or a level or field changed) | `logtest.Contract` (`internal/logtest/contract.go`) and the Messages table in `docs/logging.md` | `internal/logx/schema_test.go`, and `cmd/api/logcontract_test.go`, which runs the whole service |
| A log field name | A `Key` constant in `internal/logx/keys.go` and the Fields table in `docs/logging.md` | `internal/logx/schema_test.go`; a raw string key fails the build |
| A feature flag | `flagd/flags.json`, and `featureflag.Declare` in code | The service won't start if a declared flag is missing; `cmd/api/main_test.go` |
| A header rule or default limit (`headerguard.DefaultPolicy`) | `docs/openapi.json` (the 400, 415 and 431 responses) and the Request headers section of the README | `cmd/api/openapi_test.go` produces each status on every operation; `integration/features/headers.feature` |
| A store implementation | Nothing: run it through `itemtest.StoreContract` | The same contract `httpstore.Store` and the in-memory fake pass |
| An environment variable (or its default) | `.env.example` and the Configuration table in the README | `cmd/api/envexample_test.go`: the variables the code reads, the two lists and their defaults must all agree |

Recipes: [adding a log line](logging.md#rules-for-log-lines),
[adding a flag](feature-flags.md#adding-a-flag), and [adding a scenario](#adding-a-scenario).

The handlers in `internal/item` depend only on the `Store` interface. To add a new backing store,
implement `Store` (in its own package, like `internal/item/httpstore`) and call
`itemtest.StoreContract` from its tests: the contract that `httpstore.Store` and the in-memory
`itemtest.MemStore` both pass.

## Tests

```sh
make test         # go test ./...
make check        # gofmt check, go vet for both modules, and go test -race: what CI's fast job runs
```

The tests don't need Docker. `httpstore.Store` and the API are tested against an in-process upstream
(`net/http/httptest` serving the item routes over `itemtest.MemStore`, an in-memory test fake).
Beyond the unit tests, this includes the checks in the table above: the OpenAPI drift test, the
log contract test (the whole service, with secrets planted to prove they never reach a log line),
and the log schema tests.

One opt-in test checks `httpstore.Store` against the running WireMock, to catch drift between the stubs
and what the API's client expects. It's skipped unless `LIVE_UPSTREAM_URL` is set:

```sh
make live-test    # starts the WireMock container, then runs the Live test against it
```

## Integration tests

End-to-end scenarios, written in Gherkin in `integration/features/`, run with
[Godog](https://github.com/cucumber/godog) against the whole stack: the API container built from
the Dockerfile, WireMock and flagd.

```sh
make integration          # start the test stack, run the suite, always stop the stack
```

Or step by step, which is quicker when running the suite repeatedly:

```sh
make integration-up       # docker compose -f compose.yaml -f compose.integration.yaml up -d --build --wait
make integration-test     # go test -C integration ./... -v -count=1
make integration-down
```

If the suite fails, `make integration` prints the stack's logs before stopping it;
`make integration-logs` prints them for a stack you started with `integration-up`.

What they cover, in `integration/features/`:

- `health.feature` and `items.feature`: the health check, and each items call on its own.
- `validation.feature`: bodies refused with 400 and 422, including the 200-character name limit.
- `faults.feature`: each [WireMock fault](local-stack.md#fault-injection), switched on through
  WireMock's admin API.
- `flags.feature`: the `items-list-total` flag on and off.
- `correlation.feature`: the `Client-Correlation-Id` echoed when valid and replaced when not, on
  success and error responses alike. The header's name and format are read from
  `docs/openapi.json`, so the scenarios hold the spec and the service to each other.

How the suite works:

- `integration/` is its own Go module, so Godog stays out of the service, and `go test ./...` at
  the root doesn't run these tests or need Docker.
- `compose.integration.yaml` adjusts the stack for testing: a 3s `UPSTREAM_TIMEOUT` (so the
  timeout fault gives a 504 quickly), JSON logs, and flagd reading the tests' own copy of the flag
  definitions, `integration/flagd/flags.json`, instead of `flagd/flags.json`.
- The suite checks the stack is up before starting, and resets WireMock before every scenario, so
  a fault left switched on can't affect a run. Scenarios run one at a time because WireMock faults
  and flag values are shared.
- Flag steps edit the tests' copy and wait until flagd serves the new value. The suite resets the
  copy from `flagd/flags.json` when it starts, and after any scenario that changed it, so the copy
  always mirrors what ships and an interrupted run can't leave a flag switched. Because this edits
  a file, the tests must run on the same machine as the stack. The file is edited in place, never
  replaced, because flagd can lose its watch on a replaced file (see `integration/flags_test.go`).
- Not covered here, because it would need Docker: flagd being down (the unit tests cover the
  fallback), and the correlation ID on log lines (the log contract test covers it).
- WireMock doesn't store anything, so scenarios check each call on its own. A create followed by a
  read of the same item can't pass against this stack.
- Godog's flags work with a `godog.` prefix, e.g. `-godog.tags=@smoke` for the quick checks, or
  `-godog.format=progress` for compact output. Through make: `make integration-test TAGS=@smoke`,
  or `ARGS=-godog.format=progress` for any other flag.
- `API_URL`, `WIREMOCK_URL` and `FLAGD_URL` point the suite elsewhere (defaults:
  `localhost:8080`, `:8081` and `:8016`).

### Adding a scenario

Write it in a `.feature` file using the existing steps (listed in `integration/steps_test.go`).
An undefined step fails the run, and Godog prints a snippet to start its definition from. A
value with a double quote in it can't go inside a step's quoted argument; give it its own step.

## Continuous integration

`.github/workflows/ci.yml` runs on every push to `main` and every pull request, in three jobs, each
step a `make` target:

- **test**: `make fmt-check`, `make vet` and `make test-race` (together, `make check`). Fast, no
  Docker.
- **integration**: `make integration-up`, `make integration-test`, `make integration-logs` if
  anything failed, and always `make integration-down`. The commit SHA is passed as `VERSION`, so
  the API's log lines say which build they came from.
- **docs**: `make docs-check`, which builds the documentation website and checks every page and
  link in it (`docs/site/site_test.go`).

A newer push to the same branch cancels the run in progress.

## Open ideas

Not planned, but considered:

- **OpenAPI checks in the integration tests**: a step checking the real container's response
  bodies against `docs/openapi.json`. The in-process OpenAPI test already checks every documented
  response, so this adds little, and it would mean sharing that test's schema validator with the
  integration module.
- **OpenTelemetry tracing** (`trace_id`/`span_id`): needs third-party dependencies. The
  correlation ID covers this service; trace context can be added later through the same
  context-aware log handler.
- **Changing the log level at runtime** (`slog.LevelVar` and an admin endpoint).
- **Log sampling.**
