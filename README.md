# golang-vibe-starter

[![CI](https://github.com/ajlatour/golang-vibe-starter/actions/workflows/ci.yml/badge.svg)](https://github.com/ajlatour/golang-vibe-starter/actions/workflows/ci.yml)

A Go HTTP API built on the standard library only: no third-party dependencies. It serves `/items`
and gets the data from an upstream service over HTTP. In local development that upstream is
[WireMock](https://wiremock.org), serving stub responses, and feature flags come from
[flagd](https://flagd.dev).

```
client ──▶ api :8080 ──HTTP──▶ mock (WireMock) :8080, host :8081
           HTTPStore  │         stubs in wiremock/mappings
                      │
                      └─OFREP─▶ flagd :8016 (OpenFeature sidecar)
                                flags in flagd/flags.json
```

The upstream is set by `UPSTREAM_URL`. To use a real backend that serves the same `/items`
contract, change that variable; no code changes are needed.

## Prerequisites

- Go 1.27 or later. If your installed `go` is older, `GOTOOLCHAIN=auto` (the default) switches to
  the version pinned in `go.mod`.
- Docker with Compose, to run the local stack and the integration tests. The unit tests don't
  need it.
- `make` (GNU Make 3.81 or later, already on macOS and most Linux systems) for the shortcuts below.
  Without it, each target in the `Makefile` is a few lines you can run by hand.

Nothing else: the spec tools run in Docker, and the integration tests' Go dependencies download
on first use.

## Run

```sh
make up                                    # API, WireMock and flagd, in the background
curl localhost:8080/items                  # through the API
curl localhost:8081/items                  # WireMock directly
make fault FAULT=unavailable               # make the upstream fail; make fault-reset to undo
make down
```

`make help` lists every shortcut. [The local stack](docs/local-stack.md) covers running the API
outside Docker, WireMock's stubs, upstream faults, and load testing.

## Test

```sh
make test           # unit tests: fast, no Docker
make check          # what CI's fast job runs: gofmt, vet and the race-enabled unit tests
make integration    # starts the stack, runs the integration tests, stops the stack
```

CI runs these same targets on every push to `main` and every pull request.
[Development](docs/development.md) explains the tests, and which documents they keep in step with
the code.

## API

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

Every response carries a `Client-Correlation-Id` header. Send your own to tie the service's log
lines to yours, or quote the one returned when reporting a problem.

The full contract is in [`docs/openapi.json`](docs/openapi.json), an OpenAPI 3.0 spec: every route,
request and response schema, status code and header. `make spec` renders it as a web page,
`bin/openapi.html`, or open it in any OpenAPI viewer. It is JSON rather than YAML because the
project is standard library only, and Go has no YAML parser. Tests run the real handlers against
it, so it can't drift from the code.

## Configuration

The API reads these environment variables:

| Variable              | Default    | Description                                                |
| --------------------- | ---------- | ---------------------------------------------------------- |
| `UPSTREAM_URL`        | (required) | Base URL of the items upstream. May include a path prefix, but no query string or fragment. |
| `UPSTREAM_TIMEOUT`    | `5s`       | Timeout for each upstream request.                         |
| `FLAGS_URL`           |            | flagd OFREP base URL, e.g. `http://flagd:8016`, without a query string or fragment. Unset disables flags. |
| `FLAGS_TIMEOUT`       | `500ms`    | Timeout for each flag evaluation.                          |
| `FLAGS_FALLBACK_FILE` |            | flagd-format flag file whose defaults replace the embedded `flagd/flags.json`. |
| `ADDR`                | `:8080`    | Listen address. Takes precedence over `PORT`.              |
| `PORT`                |            | Listen port, used only when `ADDR` is unset.               |
| `LOG_LEVEL`           | `info`     | `debug`, `info`, `warn`, or `error`.                       |
| `LOG_FORMAT`          | `json`     | `json` or `text`.                                          |
| `SHUTDOWN_TIMEOUT`    | `10s`      | How long in-flight requests get to finish on SIGTERM.      |

## Documentation

- [The local stack](docs/local-stack.md): the compose stack, WireMock's stubs, fault injection,
  and load-test tuning.
- [Feature flags](docs/feature-flags.md): the flags, what happens when flagd can't answer, and
  adding a flag.
- [Logging](docs/logging.md): every field and message, how to read a request's log lines, and the
  rules for adding a log line.
- [Development](docs/development.md): the ground rules, what to update when you change something,
  and the unit, integration and CI tests.
- [`docs/openapi.json`](docs/openapi.json): the API spec.
- [`CHANGELOG.md`](CHANGELOG.md): what changed.

## Layout

```
cmd/api/                 API service: config, wiring, Dockerfile
internal/item/           Item model, Store interface, /items handlers,
                         HTTPStore and UpstreamError (the API's upstream client)
internal/httpx/          HTTP plumbing: env config, graceful Serve, middleware
                         (correlation IDs, request logging), JSON helpers, probe
internal/logx/           Logging toolkit: logger, the Key field names, request-ID
                         context, redaction and truncation helpers. Imports nothing
                         from this module.
internal/flags/          OFREP client for the flagd sidecar, flag declarations,
                         and fallback defaults derived from flagd/flags.json
internal/jsoncall/       One JSON-over-HTTP call: encode, send, redact, cap, decode, and
                         classify failures. Used by HTTPStore and the flags client.
internal/item/itemtest/  MemStore: in-memory Store, used only by tests
internal/logtest/        Capture and parse JSON logs in tests; the log contract
internal/nettest/        Dead, hanging, dropping and failing upstream stubs for tests
flagd/                   Feature flag definitions: served by flagd, and embedded
                         in the API as its fallback values
wiremock/mappings/       WireMock stubs (mounted)
wiremock/faults/         Opt-in fault stubs (loaded via the admin API)
docs/                    The documents above, and the OpenAPI spec
Makefile                 Shortcuts for running, testing and checking (make help)
integration/             Godog end-to-end tests against the composed stack (own Go module)
compose.yaml             Local stack: api + WireMock + flagd
compose.integration.yaml Compose overrides for the integration tests
.github/workflows/       CI: unit tests, then the integration tests against the stack
```
