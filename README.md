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

```sh
docker compose up --build
curl localhost:8080/items          # through the API
curl localhost:8081/items          # the mock directly
docker compose down
```

The mock must pass its healthcheck before the API starts, and both services have healthchecks.
The images are distroless and static (about 16 MB). They contain no shell or curl, so each binary checks its own health with `/<binary> healthcheck`.

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
compose.yaml      Local stack
```

The handlers in `internal/item` depend only on the `Store` interface. To add a new backing store,
implement `Store` and run it through `testStore` in `internal/item/store_test.go`, the contract
that `MemStore` and `HTTPStore` both pass.
