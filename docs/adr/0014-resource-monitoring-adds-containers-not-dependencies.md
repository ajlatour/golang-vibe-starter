# ADR-0014: Resource monitoring adds containers, not dependencies

- **Status:** Accepted
- **Date:** 2026-10-05 (04e0aa5)

**Context.** Load tests need CPU and memory figures beside their latencies. Exporters and client
libraries would add dependencies to the service.

**Decision.** Three layers: the service serves the Go runtime's own metrics with the standard
library (ADR-0022); every load run samples `docker stats` and the service's metrics before and
after; and `make monitor` starts cAdvisor, Prometheus and Grafana as an opt-in compose profile,
with a dashboard kept in the repository.

**Consequences.** The pull request report includes CPU per request and peak memory. The dashboard
is local-only and on loopback. cAdvisor needs privileged access to the Docker engine, so it runs
only when asked for.
