# golang-vibe-starter

Go HTTP microservices built on the standard library only: no third-party dependencies.

There are two services:

- **api** (`cmd/api`): the service being built. It serves `/items` and gets the data from an upstream service over HTTP.
- **mock** (`cmd/mock`): a stand-in for that upstream. It serves `/items` from memory, starts with seeded sample data, and resets on restart.

```
client ──▶ api :8080 ──HTTP──▶ mock :8080 (host :8081)
           HTTPStore            MemStore
```

The API's upstream is set by `UPSTREAM_URL`. To use a real backend that serves the same `/items` contract, change that variable; no code changes are needed.

## Run with Docker Compose

There are two upstream mocks. The API code is the same with either; only `UPSTREAM_URL` differs.

| Stack             | Command                                  | API     | Upstream               | Use for                                 |
| ----------------- | ---------------------------------------- | ------- | ---------------------- | --------------------------------------- |
| Go mock (default) | `docker compose up --build`              | `:8080` | Go mock on `:8081`     | Everyday development and load testing   |
| WireMock profile  | `docker compose up --build api-wiremock` | `:8090` | WireMock on `:8082`    | Fault injection and declarative stubs   |

### Default: Go mock

```sh
docker compose up --build
curl localhost:8080/items          # through the API
curl localhost:8081/items          # the mock directly
docker compose down
```

The mock must pass its healthcheck before the API starts, and both services have healthchecks.
The images are distroless and static (about 16 MB). They contain no shell or curl, so each binary checks its own health with `/<binary> healthcheck`.

**Use this stack for load and stress testing.** The Go mock is a small static binary with a
predictable cost per request, so the numbers you get reflect the API rather than the mock.

### WireMock profile: fault injection

```sh
docker compose up --build api-wiremock     # starts only api-wiremock + wiremock
curl localhost:8090/items                  # API, backed by WireMock
curl localhost:8082/__admin/mappings       # WireMock admin API
docker compose --profile wiremock down     # plain `down` leaves the profile's containers running
```

Targeting `api-wiremock` turns the `wiremock` profile on, so only that API and WireMock start.
Compose profiles can only add services, never remove ones that have no profile. So instead of
re-pointing the default API, WireMock gets its own API instance on its own port. Each API has
exactly one upstream, and no two services share a host port. That also means
`docker compose --profile wiremock up` can run both stacks side by side if you want to compare them.

**Stubs** live in `mocks/wiremock/mappings/` and are mounted read-only. They cover the same `/items`
contract as the Go mock: list, get, create, update, delete, 404 for unknown IDs, 422 for invalid
names, 400 for malformed bodies, and 405 for other methods. Through the API, every endpoint and
error case returns the same status and response shape with either upstream.
Differences to be aware of:

- **WireMock keeps no state.** It always lists `widget`, `gadget` and `gizmo`. Create returns
  `new-item`, which echoes the `name` and `description` you sent, but a later GET of it returns 404.
  Deletes don't remove anything.
- **Reads are fully static.** Create and update use response templating so the reply echoes your
  input; those requests aren't the load-test hot path.

To reload stubs after editing the files: `curl -X POST localhost:8082/__admin/mappings/reset`.

**Faults** live in `mocks/wiremock/faults/`. They aren't mounted, so none are active by default.
Each file is a single stub with a fixed `id` and the highest priority, so it overrides the
happy-path stub while it's loaded:

| File                    | Effect on the upstream                          | What the API does                    |
| ----------------------- | ----------------------------------------------- | ------------------------------------ |
| `slow-list.json`        | `GET /items` takes 2s                           | 200 after ~2s                        |
| `timeout-list.json`     | `GET /items` takes 8s                           | 500 after 5s (`UPSTREAM_TIMEOUT`)    |
| `unavailable.json`      | Every `/items` request returns 503              | 500, logs the upstream 503           |
| `connection-reset.json` | Every `/items` connection is reset              | 500, logs the connection reset       |
| `malformed-list.json`   | `GET /items` returns truncated JSON             | 500, logs the decode error           |

```sh
# turn a fault on
curl -X POST localhost:8082/__admin/mappings -H 'Content-Type: application/json' \
  --data @mocks/wiremock/faults/unavailable.json
# turn it off again (the ID is in the file)
curl -X DELETE localhost:8082/__admin/mappings/f0000000-0000-4000-8000-000000000003
# or drop every runtime change and go back to the mounted stubs
curl -X POST localhost:8082/__admin/mappings/reset
```

**Tuning.** WireMock's defaults suit unit-test-style verification, not long or high-volume
runs. `compose.yaml` sets:

- `--no-request-journal`: by default WireMock keeps every request in memory for later
  verification, and the heap grows without limit over a long run. The trade-off is that
  `/__admin/requests` and the other verification endpoints return an error. Remove the flag
  temporarily if you need to inspect requests.
- `--disable-request-logging`: no log line per request.
- `--async-response-enabled=true` and `--async-response-threads=32`: delayed responses (the
  slow and timeout faults) wait on a separate pool instead of holding Jetty request threads.
- `--container-threads=200`: a larger Jetty request thread pool, for high concurrency.
- `--disable-gzip`: the Go mock doesn't compress responses, so neither does WireMock. That keeps
  CPU cost comparable and avoids gzip work on both sides.
- `JAVA_OPTS=-Xms512m -Xmx512m`: a fixed heap, so it never pauses to resize mid-run.

The image is pinned to `wiremock/wiremock:3.13.2`. The `-alpine` variant has no arm64 build, and
running it under emulation on Apple Silicon would distort timings.

**Measuring tail latency against WireMock:** it runs on the JVM. The first requests run before the
JIT compiler has warmed up, and garbage-collection pauses arrive in bursts. Both show up in p99
and p99.9 as the mock's latency, not your API's. If you load test this profile anyway, run a warm-up
phase first and leave it out of the results, and compare p99 against the same run on the Go mock
before blaming the API. WireMock also competes with the API and the load generator for CPU, so
consider capping it with `cpus:` in compose.

## Run without Docker

```sh
PORT=8081 go run ./cmd/mock
UPSTREAM_URL=http://localhost:8081 go run ./cmd/api
```

Go 1.27+ is required. If your installed `go` is older, `GOTOOLCHAIN=auto` (the default) switches to the version pinned in `go.mod`.

## Test

```sh
go test ./...
```

The tests don't need Docker. Wherever a test needs an upstream, it starts one in-process with `net/http/httptest`.

## Configuration

Both services read these environment variables:

| Variable           | Default | Description                                            |
| ------------------ | ------- | ------------------------------------------------------ |
| `ADDR`             | `:8080` | Listen address. Takes precedence over `PORT`.          |
| `PORT`             |         | Listen port, used only when `ADDR` is unset.           |
| `LOG_LEVEL`        | `info`  | `debug`, `info`, `warn`, or `error`.                   |
| `LOG_FORMAT`       | `json`  | `json` or `text`.                                      |
| `SHUTDOWN_TIMEOUT` | `10s`   | How long in-flight requests get to finish on SIGTERM.  |

The API also reads these:

| Variable           | Default    | Description                                                |
| ------------------ | ---------- | ---------------------------------------------------------- |
| `UPSTREAM_URL`     | (required) | Base URL of the items upstream. May include a path prefix. |
| `UPSTREAM_TIMEOUT` | `5s`       | Timeout for each upstream request.                         |

## Endpoints

Both services serve these:

| Method | Path          | Success | Description                                   |
| ------ | ------------- | ------- | --------------------------------------------- |
| GET    | `/healthz`    | 200     | Liveness: `{"status":"ok"}`.                  |
| GET    | `/items`      | 200     | List items: `{"items":[...]}`, oldest first.  |
| POST   | `/items`      | 201     | Create an item. Sets a `Location` header.     |
| GET    | `/items/{id}` | 200     | Get one item.                                 |
| PUT    | `/items/{id}` | 200     | Replace an item's `name` and `description`.   |
| DELETE | `/items/{id}` | 204     | Delete an item.                               |

`items` is a mock resource standing in for the real domain. Request bodies are JSON
(`{"name": "...", "description": "..."}`). `name` is required, at most 200 characters,
and trimmed of surrounding whitespace. Bodies are limited to 1 MiB, and unknown fields are rejected.

Errors are JSON, `{"error":"..."}`:

- 400: malformed body
- 404: unknown ID
- 422: validation failure
- 500: unexpected failure, including the upstream being unreachable; details are logged, not returned

## Layout

```
cmd/api/          API service: config, wiring, Dockerfile
cmd/mock/         Mock upstream: seed data, wiring, Dockerfile
internal/httpx/   Shared plumbing: env config, graceful Serve, middleware,
                  JSON helpers, healthcheck probe
internal/item/    Item model, Store interface, /items handlers,
                  MemStore (mock) and HTTPStore (API → upstream)
mocks/wiremock/   WireMock stubs (mappings/, mounted) and opt-in faults (faults/)
compose.yaml      Local stack: default Go mock, optional WireMock profile
```

The handlers in `internal/item` depend only on the `Store` interface. To add a new backing store,
implement `Store` and run it through `testStore` in `internal/item/store_test.go`, the contract
that `MemStore` and `HTTPStore` both pass.
