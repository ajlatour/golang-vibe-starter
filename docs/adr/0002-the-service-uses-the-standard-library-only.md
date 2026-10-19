# ADR-0002: The service uses the standard library only

- **Status:** Superseded by ADR-0046
- **Date:** 2026-10-03 (029a3cd)

**Context.** Every dependency is code to audit, update and trust. Go's standard library covers
HTTP serving and clients, JSON, structured logging (`log/slog`), runtime metrics and profiling.

**Decision.** The service's module has no third-party dependencies. Where a tool would normally
bring one, the code does the job itself: flags over plain HTTP instead of an OpenFeature SDK
(ADR-0005), the API spec in JSON instead of YAML (ADR-0008), metrics in Prometheus's text format
written by hand (ADR-0022). Tooling that needs a dependency gets a module of its own: the
integration tests (Godog) and the performance report (benchstat's statistics).

**Consequences.** Nothing to update but Go. Some code exists that a library would otherwise
provide, kept small and tested. `docs/architecture_test.go` fails if the service's `go.mod` gains
a requirement.
