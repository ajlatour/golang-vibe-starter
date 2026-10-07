# ADR-0022: Metrics are Prometheus text, served privately

- **Status:** Accepted
- **Date:** 2026-10-05 (04e0aa5, 8b47875)

**Context.** Operators need request rates, errors and latencies, and the runtime's state. The
Prometheus client library would be a dependency (ADR-0002). Profiles and metrics also reveal
internals.

**Decision.** Write Prometheus's text format with the standard library: every scalar from
`runtime/metrics`, and per-route request counts and latency histograms (`internal/httpmetrics`)
with bounded labels and no allocation per request. Serve them, with pprof, on `PPROF_ADDR` only:
off by default, never the API's port.

**Consequences.** Any Prometheus can scrape the service (OBS-3), and `make monitor` does. Names
follow the Prometheus Go client's, so dashboards carry over. Exposure is the deployment's to keep
private; the service warns when it listens beyond loopback (SEC-6).
