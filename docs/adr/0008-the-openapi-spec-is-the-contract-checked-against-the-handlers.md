# ADR-0008: The OpenAPI spec is the contract, checked against the handlers

- **Status:** Accepted
- **Date:** 2026-10-04 (2e0c9c8)

**Context.** An API spec that's written by hand drifts from the code; one generated from code
needs annotations and tooling.

**Decision.** `docs/openapi.json` is written by hand, in JSON because the standard library has no
YAML parser. Tests run the real handlers through every documented response of every operation and
check status, body schema, headers and limits against the spec, and that the code's routes and
the spec's are the same.

**Consequences.** Changing the API means changing the spec in the same pull request, or a test
fails (API-1). The tests validate with a small JSON Schema subset written for them, so a schema
feature they don't understand fails loudly rather than passing.
