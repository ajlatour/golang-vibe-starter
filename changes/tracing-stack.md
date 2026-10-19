### Added

- `make tracing` starts the local stack with Jaeger, and the API sending it a span for every
  request and every call it makes: the traces are at http://localhost:16686. `make down` stops it
  with the rest.

### Changed

- The probes, `/healthz` and `/readyz`, record no spans (`httpx.WithSpans` takes paths to exempt),
  so a container's healthcheck doesn't fill the traces.
