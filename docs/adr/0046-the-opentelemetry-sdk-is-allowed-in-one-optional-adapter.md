# ADR-0046: The OpenTelemetry SDK is allowed in one optional adapter

- **Status:** Accepted
- **Date:** 2026-10-18

**Context.** The service carries W3C trace context through every call
([ADR-0034](0034-w3c-trace-context-is-carried-through-without-a-tracing-sdk.md)), but records no
spans: where a request's time went, across this service and the ones it calls, is guesswork. The
OpenTelemetry Go SDK records them, and it's third-party code, which
[ADR-0002](0002-the-service-uses-the-standard-library-only.md) rules out. Its OTLP exporter is
worse: it encodes protobuf, and links in gRPC, protobuf and about a dozen more modules, doubling
the binary (8 MB to 15 MB), tracing on or off. Recording spans by hand would mean sampling,
batching and the span model by hand too. And a deployment may have nowhere to send spans at all,
so tracing must cost nothing when it's off, and be easy to take out of a service that will never
use it.

**Decision.** Third-party code is allowed in adapter packages named in
`docs/architecture_test.go`, each importing only the modules listed for it, and so far there's one:
`internal/telemetry`, the OpenTelemetry SDK (its API, `sdk` and `trace` modules). It exports over
OTLP/HTTP in OTLP's JSON encoding, written with `encoding/json` as the Prometheus format is
([ADR-0022](0022-metrics-are-prometheus-text-served-privately.md)), not with the SDK's
protobuf exporter. Everything else stays standard library only. The service tells it of spans through a standard-library interface,
`tracectx.Spans`, which the middleware and `jsoncall` call; `cmd/api` wires the adapter in only when
`OTEL_EXPORTER_OTLP_ENDPOINT` (or the traces one) is set. It keeps the trace IDs the service already
logs, a root span's included. This supersedes ADR-0002.

**Consequences.** Spans, with the trace IDs the logs already carry, when there's a collector to
send them to; with tracing off, nothing of the SDK runs, and the request path is as before. The
module now has eight dependencies, the SDK's, to update (Dependabot) and check (govulncheck), and
the binary is about 1 MB bigger. A backend that takes OTLP only as protobuf needs a Collector in
front of it; the Collector, Jaeger and most backends take JSON. Removing tracing is deleting `internal/telemetry` and `cmd/api/telemetry.go`, and what the
compiler and the drift tests then name ([Tracing](../tracing.md#removing-it)). Another adapter
with a dependency takes a record of its own, and a line in the test.
