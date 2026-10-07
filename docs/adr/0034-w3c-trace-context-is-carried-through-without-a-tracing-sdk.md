# ADR-0034: W3C trace context is carried through, without a tracing SDK

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** OpenTelemetry is planned, but its Go SDK would be the service's first dependency
(ADR-0002), and until it's added, a request that crosses this service loses its trace: the calls
the service makes to the upstream, the validation service and flagd start no trace, or a new one.
Carrying the trace context through is the part of tracing that needs no SDK.

**Decision.** `internal/tracectx` implements W3C Trace Context's `traceparent`: the middleware takes
a request's from a valid header, or starts a trace (unsampled, since nothing records it), and keeps
it in the request's log state (`logx.SetTrace`), so every log line of the request carries
`trace_id`. Every outbound call, through `jsoncall`, sends `traceparent` with the request's trace
and flags and a span ID of its own. `tracestate` is not carried, and no spans are recorded.

**Consequences.** A caller's trace ID finds this service's log lines for its request, and the
upstream and the validation service see the same trace. When an OpenTelemetry SDK is added, its
spans join traces that already flow, and it replaces the generated span IDs with real ones. Each
request costs an allocation for its trace ID's text, and each outbound call one for its header.
