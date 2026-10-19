# Development

How the code is put together, how to change it safely, and how it's tested.

- [Starting a new service](#starting-a-new-service)
- [Adding a resource](#adding-a-resource)
- [Ground rules](#ground-rules)
- [Making a change](#making-a-change)
- [Tests](#tests)
- [Integration tests](#integration-tests)
- [Continuous integration](#continuous-integration)
- [Open ideas](#open-ideas)

<!-- feature:init -->
## Starting a new service

This repository is a template: a new service starts as a copy of it, with GitHub's "Use this
template" or a plain clone, and then one command makes the copy its own:

```sh
make init NAME=orders MODULE=github.com/acme/orders                   # everything
make init NAME=orders MODULE=github.com/acme/orders WITHOUT=tracing   # leaving tracing out
```

`NAME` is the service's name, in lower-case letters, digits and dashes: it names the images, the
compose project and the API in its spec. `MODULE` is its Go module path. `make init`
(`cmd/initservice`) rewrites every file under version control:

- the module path becomes `MODULE`, in every `go.mod` and import;
- the template's GitHub repository becomes `MODULE`'s, when that's on github.com, so the
  workflows that check which repository they run in still run;
- the template's name becomes `NAME`;
- `.github/CODEOWNERS` names `MODULE`'s GitHub owner, or, off GitHub, is left for you to fill in.

And it starts the service's own history: `CHANGELOG.md` holds one release, 0.0.0, saying which
version of the template it came from; `changes/` is emptied; the spec's version is 0.0.0; and the
template's audit page goes. The example `/items` resource, the decision records and the license
stay. It prints what's left to do by hand: the license's holder, the repository's settings, the
README's opening, and when to remove `/items`.

Some of the template is optional, and a service can leave it out: `WITHOUT=tracing`, or several,
comma-separated, or `WITHOUT=all`. `go run ./cmd/initservice -list-features` lists them:

| Feature | What goes |
| ------- | --------- |
| `tracing` | OpenTelemetry ([Tracing](tracing.md)): `internal/telemetry`, its settings and log lines, and Jaeger in the local stack. The service then has no third-party dependency at all. |
| `perf` | Performance testing ([Performance](performance.md)): `perf/`, the k6 load profiles and the report tool; the Performance, Performance trends, Soak and CI times workflows; `monitoring/` and its profile in the local stack. The Go benchmarks stay. |
| `validation` | Request validation ([Request validation](request-validation.md)): `internal/validator`, its settings, log lines, metrics, readiness check and error codes, its flag, and WireMock's stub of the service. |
| `flags` | Feature flags ([Feature flags](feature-flags.md)): `internal/featureflag`, `flagd/` and the flagd sidecar, their settings, log lines and integration steps, and the example list's `total`. Request validation, if kept, then always acts on the service's answers: there's no shadow mode without a flag to turn it off. |
| `release` | Publishing ([Releasing](releasing.md)): the Release workflow (the multi-platform image, its SBOM and provenance, the smoke test, GHCR and the GitHub release), `make smoke` and `scripts/smoke-image.sh`. Versioning stays: the changelog, `make release-prepare` and `release-tag`. |

A feature is marked in the code where it's mixed with the rest (`// feature:tracing` …
`// end feature:tracing`, or `#` and `<!-- -->` in other files), and listed in
`cmd/initservice/features.go` where it's files of its own, lines of a table, or text that reads
differently without it. A service gets neither: `make init` removes the markers, of what it keeps
as well as what it leaves out, and its own tool, `cmd/initservice`.

Run it once, on a fresh copy, then `make check`, `make lint` and `make docs-check`, and commit.
`make init-check`, which CI runs, does all that on copies of the template, with every feature,
with each left out, and with all of them left out, and checks nothing still names what was left
out; `make init-docs-check` runs shellcheck and actionlint on, and builds the docs of, a service
with each left out and with all of them, so `make init` keeps working as the template changes.
A block marked `feature:!perf` (with a `!`) is the opposite: what's there instead without `perf`,
its lines commented out in the template so it does nothing there. Imports only a left-out block
used go too.

<!-- end feature:init -->

## Adding a resource

```sh
make resource NAME=order                         # /orders
make resource NAME=category PLURAL=categories    # when the plural isn't NAME with an s
```

`make resource` (`cmd/newresource`) copies everything the example resource, `/items`, owns, with
its names changed (for `NAME=order`, `item` to `order`, `Items` to `Orders`, `ITEM` to `ORDER`):

- its package, `internal/<name>`: the model, the `Store` interface, the handlers, the stores in
  memory and on the upstream, and the contract every store passes;
- its wiring, `cmd/api/<plural>.go`, and what the spec tests need of it,
  `cmd/api/<plural>_test.go`: its stores for each scenario and its own scenarios;
- its WireMock stubs, `wiremock/mappings/<plural>-*.json`, and its integration feature.

And it adds the new resource to each list of them: `cmd/api/resources.go` and
`cmd/api/resources_test.go`, the spec's paths and schemas, the feature flags (`flagd/flags.json`, the
integration stack's, and the table in [Feature flags](feature-flags.md)), the load test's traffic
mix, an example of each call in [Calling the API](curl.md), and the architecture's package table.

So the new resource builds and passes every check as generated, serving what `/items` does under
its own name. Then make it the resource you mean, as `make resource` lists: its fields (the model,
`Request` and `Response`, and the spec's schemas, which the tests hold to each other), its store,
its flag, and its examples. `make resource-check`, which CI runs, adds one to a copy of the
template and runs the tests, so it keeps working as `/items` changes.

## Ground rules

- **The service uses the standard library only**, but for adapters allowed a dependency: so far one,
  `internal/telemetry`, the OpenTelemetry SDK, optional and removable
  ([ADR-0046](adr/0046-the-opentelemetry-sdk-is-allowed-in-one-optional-adapter.md)). Where a tool
  would normally pull one in, the code does the job itself: flags over plain HTTP (OFREP) instead
  of an OpenFeature SDK, a JSON API spec instead of YAML.
- **Test-only dependencies live in their own module.** The integration tests need Godog, so they
  are a separate Go module in `integration/`, and the service never depends on it.
- **Documentation that describes the code is checked against it.** The API spec, the log tables,
  the flag definitions and the store contract each have tests that fail when the code and the
  description disagree. The next section lists them.
- **Tests never call Docker.** The integration tests talk HTTP to a stack you start yourself.
- **The architecture is written down and checked.** [Architecture](architecture.md) has the
  package dependency rules and the requirements; [decisions](decisions.md) records why.
  `docs/architecture_test.go` fails when a package imports what the table doesn't allow, or a
  record is incomplete. A pull request that makes an architectural choice adds a record.
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
| A log message (new, renamed, or a level or field changed) | `logtest.Contract` (`internal/logtest/contract.go`) and the Messages table in `docs/logging.md`; and `logtest.OutsideRequests` for one logged outside any request (without a context, or with `context.Background`) | `internal/logx/schema_test.go`, and `cmd/api/logcontract_test.go`, which runs the whole service |
| A log field name | A `Key` constant in `internal/logx/keys.go` and the Fields table in `docs/logging.md` | `internal/logx/schema_test.go`; a raw string key fails the build |
| A feature flag | `flagd/flags.json`, and the package's `FlagKeys` in code | The service won't start if a listed flag is missing; `cmd/api/main_test.go` |
| A header rule or default limit (`headerguard.DefaultPolicy`) | `docs/openapi.json` (the 400, 415 and 431 responses) and the Request headers section of the README | `cmd/api/openapi_test.go` produces each status on every operation; `integration/features/headers.feature` |
| A WireMock stub of the upstream (`wiremock/`), or the `/items` contract | The stubs, to the contract: a success in the spec's status and body schema, an error as `{"error": "..."}`, a 404 as JSON | `cmd/api/openapi_stubs_test.go` checks every stub's route, status and body against `docs/openapi.json`, and that every operation's success, and an item's 404, is stubbed |
| A setting's rules: its default, a check across settings, or one removed or renamed | The configurations in `cmd/api/testdata/configs` if a deployment's must change, and the changelog | `cmd/api/configs_test.go` starts the service with each, and with every documented default; it fails on one refused, or setting a variable the API no longer reads |
| `docs/openapi.json`, in a way that may break clients (see [Releasing](releasing.md#what-keeps-a-release-safe-to-deploy)) | A `**Breaking**` entry in a change file in `changes/`, saying what breaks | `make api-compat` (`cmd/apidiff`, against the last release's spec); CI's docs job runs it |
| An image the build runs (a tool in the Makefile or a workflow) | `tools/images/Dockerfile`, pinned to a digest, read with `scripts/image.sh <name>`, so Dependabot keeps it current | `docs/pins_test.go` fails on a pinned image anywhere Dependabot doesn't look, or a listed one nothing uses |
| A store implementation | Nothing: run it through `itemtest.StoreContract` | The same contract `httpstore.Store` and the in-memory fake pass |
| A route, status or header rule a caller would hit, or `scripts/api` | `docs/curl.md`, which needs a successful call of every route | `cmd/api/curl_test.go` runs every command on the page, and the script, against the real handlers |
| A benchmarked path, adding allocations on purpose | Nothing: add the `perf-ok` label to the pull request | The Performance workflow fails a pull request that allocates more per operation; see [pull request reports](performance.md#pull-request-reports) |
| A route (new or removed) | The traffic mix in `perf/k6.js` | `perf/profiles_test.go` checks the mix covers every operation in `docs/openapi.json` |
| A Go runtime metric's name, or a k6 percentile on the dashboard | `monitoring/grafana/dashboards/performance.json` | `monitoring/monitoring_test.go` checks the dashboard's metrics exist in `/metrics` and its k6 percentiles are ones `perf/run.sh` sends |
| A k6 profile or threshold | `perf/profiles.json`, and its row in `docs/performance.md` | `perf/profiles_test.go` checks phases, durations and threshold syntax |
| A package's imports (a new package, or one importing another it didn't) | The package table in `docs/architecture.md` | `docs/architecture_test.go` |
| An architectural choice: a new dependency, protocol, protection or pattern | A record, a new file in `docs/adr/` ([how](decisions.md)), superseding any it replaces | `docs/architecture_test.go` checks records are complete, named for their number and title, and numbered with no gaps or repeats |
| Anything worth telling users of a release | A change file in `changes/` ([how](../changes/README.md)), not `CHANGELOG.md` | `docs/changelog_test.go` checks every change file gathers, and that the changelog's Unreleased stays empty |
| Code that `make lint` flags, where the finding is wrong or accepted on purpose | A comment on the line, with the rule and why: `// #nosec G304 -- the path is the operator's own configuration`, `//lint:ignore SA4000 why` for staticcheck, or `# shellcheck disable=SC2086` under a comment saying why, in a script | `make lint`, in CI's lint job |
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

The stubs themselves are held to `docs/openapi.json` without Docker
(`cmd/api/openapi_stubs_test.go`, in the table above). Templated bodies are rendered with sample
values first; a template helper the test doesn't know fails it, rather than going unchecked.

One opt-in test checks `httpstore.Store` against the running WireMock, to catch drift between the stubs
and what the API's client expects. It's skipped unless `LIVE_UPSTREAM_URL` is set:

```sh
make live-test    # starts the WireMock container, then runs the Live test against it
```

### Fuzz tests

The code that handles untrusted input has fuzz tests (Go's built-in fuzzing): JSON request bodies
(`FuzzDecodeJSON`), correlation IDs (`FuzzCorrelationID`), the header guard (`FuzzMiddleware`), item
IDs (`FuzzValidID`), `API_KEYS` (`FuzzParseKeys`) and the request metrics' labels (`FuzzObserve`).
Each states a property that must hold for any input, not just "doesn't panic": the header guard
lets a request through or refuses it with 400, 415 or 431, and never passes on a header it strips;
a valid ID never needs escaping in a URL. Their seed inputs run with every `go test`. `make fuzz`
runs each target for `FUZZTIME` (30s), and CI for 10s on every push. When the fuzzer finds a
failing input it saves it under the package's `testdata/fuzz/`: commit it with the fix, and it
runs as a regression test from then on.

## Integration tests

End-to-end scenarios, written in Gherkin in `integration/features/`, run with
[Godog](https://github.com/cucumber/godog) against the whole stack: the API container built from
the Dockerfile, and its stubbed dependencies.

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

`.github/workflows/ci.yml` runs on every push to `main` and every pull request, in seven jobs, each
step a `make` target:

- **test**: `make fmt-check`, `make vet`, and `make cover`: the unit tests with the race detector,
  and the coverage they give across the service's packages (a package's code counts as covered
  when any package's tests run it), with a table of it per package in the job summary. It fails
  if the total is under the floor, `COVER_MIN` in the Makefile (90%); raise the floor as coverage
  rises, never lower it. Fast, no Docker.
- **integration**: `make integration-up`, `make integration-test`, `make integration-logs` if
  anything failed, and always `make integration-down`. The commit SHA is passed as `VERSION`, so
  the API's log lines say which build they came from.
- **docs**: `make docs-check`, which builds the documentation website and checks every page and
  link in it (`docs/site/site_test.go`).
- **template**: `make init-check` and `make resource-check`: [`make init`](#starting-a-new-service),
  and [`make resource`](#adding-a-resource), each on a copy of the repository, then the result
  vetted and its tests run, so both keep working as the template changes.
- **lint**: `make lint`, on all three modules, with the Go tools built from `tools/go.mod` (`make tools`; ADR-0045): staticcheck with every check, style included (bugs,
  suspicious code, and doc comments, which every package and exported name has); modernize (code
  a newer Go says more simply); gosec (security mistakes); `go mod tidy -diff` (`go.mod` and
  `go.sum` say exactly what the code needs); and, in Docker, shellcheck on the shell scripts and
  actionlint on the workflows.
- **fuzz**: `make fuzz FUZZTIME=10s`, a short run of every fuzz test (see [Tests](#tests)).
- **vuln**: `make vuln`, govulncheck over all three modules: known vulnerabilities in the
  dependencies and the Go standard library that the code can actually reach.

The integration and load jobs build the API's image with `scripts/build-api-image.sh`, which seeds
the Go compile inside Docker from a build cache kept between runs (`actions/cache`), so only the
packages a change touches recompile. Pull requests start from the cache `main`'s runs keep. Local
builds are unaffected: BuildKit keeps its own cache there.

Every action is pinned to a commit SHA (the version is in a comment beside it) and every container
image to a digest, so a moved tag can't change what CI runs. The runner image is pinned too
(`ubuntu-24.04`, not `ubuntu-latest`, which GitHub moves to each new release): a new one is
taken on purpose, by changing every `runs-on` in one pull request, before the pinned one is
retired. The API's image builds from an allow-list of what it needs (`.dockerignore`), so
nothing else reaches the build. Dependabot (`.github/dependabot.yml`)
opens a weekly pull request for each kind of update: Go modules, actions, and images: the
Dockerfile's, compose's, and the tools the Makefile and workflows run, listed in
`tools/images/Dockerfile` for it (`docs/pins_test.go` keeps them there).

A newer push to the same branch cancels the run in progress.

<!-- feature:release -->
`.github/workflows/release.yml` publishes a release when a version tag is pushed; see
[Releasing](releasing.md).

<!-- end feature:release -->
<!-- feature:perf -->
`.github/workflows/perf-trends.yml` runs after every merge to `main`, recording its performance on
the `perf-data` branch; see [trends](performance.md#trends).

`.github/workflows/soak.yml` runs `main` under steady load for half an hour every night, and fails
if the API's heap or goroutines grew; see [Soak](performance.md#soak).

`.github/workflows/perf.yml` runs on every pull request too: it compares the pull request's
performance with its base and keeps the result in a comment on the pull request. See
[pull request reports](performance.md#pull-request-reports).

<!-- end feature:perf -->
## Open ideas

Not planned, but considered:

- **OpenAPI checks in the integration tests**: a step checking the real container's response
  bodies against `docs/openapi.json`. The in-process OpenAPI test already checks every documented
  response, so this adds little, and it would mean sharing that test's schema validator with the
  integration module.
- **Changing the log level at runtime** (`slog.LevelVar` and an admin endpoint).
- **Log sampling.**
