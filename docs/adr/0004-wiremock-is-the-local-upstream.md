# ADR-0004: WireMock is the local upstream

- **Status:** Accepted
- **Date:** 2026-10-03 (a81d892)

**Context.** Local development and the integration tests need an upstream. A hand-written mock
service was the first stand-in; it needed code for every behaviour, faults included.

**Decision.** Use WireMock, with stubs in `wiremock/mappings` and opt-in fault stubs in
`wiremock/faults`, switched on through its admin API (`make fault`).

**Consequences.** Faults (timeouts, resets, malformed bodies) are a JSON file, not code. WireMock
keeps no state: a create returns `new-item` and nothing is stored, which the tests and guides
allow for. It's a JVM, so its warm-up and garbage collection show in load tests' tail latency.
