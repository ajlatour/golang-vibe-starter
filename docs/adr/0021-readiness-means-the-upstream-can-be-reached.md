# ADR-0021: Readiness means the upstream can be reached

- **Status:** Accepted
- **Date:** 2026-10-05 (1330a94)

**Context.** A load balancer needs to know which instances to send traffic to. If readiness
followed the upstream's health, an upstream problem would take every instance out of service at
once, turning partial failure into total outage.

**Decision.** `/readyz` is ready while the service can reach the upstream (any HTTP answer counts,
an error included) and isn't shutting down. `/healthz` stays the liveness check. Results are
cached for a second. `SHUTDOWN_DELAY` keeps serving after SIGTERM, reporting not ready.

**Consequences.** Instance-local problems (network, DNS, configuration) take an instance out of
service; upstream problems surface as 502s from every instance instead (REL-4). Deploys can drain
cleanly (REL-5).
