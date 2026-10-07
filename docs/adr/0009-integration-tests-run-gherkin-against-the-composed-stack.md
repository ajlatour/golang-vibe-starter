# ADR-0009: Integration tests run Gherkin against the composed stack

- **Status:** Accepted
- **Date:** 2026-10-04 (ef95db8)

**Context.** Unit tests run the handlers in-process. Something must also check the real
container, WireMock and flagd together, without tests that manage Docker themselves.

**Decision.** Godog (Gherkin) scenarios in `integration/`, a module of its own, talk HTTP to a
stack started separately (`make integration-up`, as CI does). Tests never call Docker.

**Consequences.** Scenarios read as behaviour, and the stack can be inspected while they run.
Godog is a dependency of the integration module only, never of the service (ADR-0002). Scenarios
assume WireMock's stubs, and that flagd reloads its file, which `integration/flags_test.go`
handles with care.
