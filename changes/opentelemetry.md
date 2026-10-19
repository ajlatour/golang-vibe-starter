### Added

- Tracing, with OpenTelemetry: set `OTEL_EXPORTER_OTLP_ENDPOINT` (or
  `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) and the service records a span for each request and each
  call it makes, in the trace IDs the logs already carry, and exports them over OTLP/HTTP, as JSON.
  OpenTelemetry's usual `OTEL_*` settings (sampling, headers, the service's name) apply. Unset, the default,
  nothing of it runs. The SDK is in one package, `internal/telemetry`, the service's only
  third-party code, which [Tracing](docs/tracing.md#removing-it) says how to remove
  (ADR-0046, superseding ADR-0002). New log lines: `using tracing` and `tracing failed`.
