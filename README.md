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

| Method | Path       | Description                    |
| ------ | ---------- | ------------------------------ |
| GET    | `/healthz` | Liveness: `{"status":"ok"}`.   |

## Layout

Everything is in `package main` at the repo root for now:

- `main.go`: entrypoint, `run()`, graceful shutdown
- `config.go`: environment config
- `server.go`: route registration (`addRoutes`) and JSON helpers
- `middleware.go`: request logging and panic recovery

New endpoints go in `addRoutes`. Once there is real domain logic, move it into its own package (for example `internal/<domain>`) instead of growing `package main`.
