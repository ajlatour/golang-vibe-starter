# golang-vibe-starter

A Go HTTP microservice built on the standard library only: no third-party dependencies.

## Requirements

Go 1.27+. If your installed `go` is older, `GOTOOLCHAIN=auto` (the default) switches to the toolchain pinned in `go.mod`.

## Run

```sh
go run .
curl localhost:8080/healthz
```

## Test

```sh
go test ./...
```

## Configuration

All configuration comes from environment variables.

| Variable           | Default | Description                                             |
| ------------------ | ------- | ------------------------------------------------------- |
| `ADDR`             | `:8080` | Listen address. Takes precedence over `PORT`.           |
| `PORT`             |         | Listen port, used only when `ADDR` is unset.            |
| `LOG_LEVEL`        | `info`  | `debug`, `info`, `warn`, or `error`.                    |
| `LOG_FORMAT`       | `json`  | `json` or `text`.                                       |
| `SHUTDOWN_TIMEOUT` | `10s`   | How long to let in-flight requests finish on SIGTERM.   |

## Endpoints

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
- 500: unexpected failure; details are logged, not returned

```sh
curl -X POST localhost:8080/items -d '{"name":"first"}'
curl localhost:8080/items
```

## Storage

Handlers depend only on the `ItemStore` interface (`items.go`). The only implementation
right now is `memStore` (`memstore.go`), which keeps data in memory, so data is lost on restart.
To add a real datastore, implement `ItemStore`, run it through `testItemStore` in
`memstore_test.go` (the shared contract test), and pass it to `newServer` in `run()`.

## Layout

Everything is in `package main` at the repo root for now:

- `main.go`: entrypoint, `run()`, graceful shutdown
- `config.go`: environment config
- `server.go`: route registration (`addRoutes`) and JSON request/response helpers
- `middleware.go`: request logging and panic recovery
- `items.go`: `Item` model, `ItemStore` interface, and item handlers
- `memstore.go`: in-memory `ItemStore`

New endpoints go in `addRoutes`. Once there is real domain logic, move it into its own package (for example `internal/<domain>`) instead of growing `package main`.
