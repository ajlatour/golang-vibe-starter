# ADR-0005: Feature flags come from flagd over OFREP, with embedded defaults

- **Status:** Accepted
- **Date:** 2026-10-03 (705e1f0, 8abd719)

**Context.** The service needs feature flags. OpenFeature is the standard; its Go SDK and
flagd's gRPC provider would be the first dependencies (ADR-0002). Flags also mustn't become a
reason for requests to fail.

**Decision.** Run flagd as a sidecar and evaluate flags over OFREP, its plain HTTP/JSON protocol.
`flagd/flags.json` is both what flagd serves and, compiled into the binary, the fallback when
flagd can't answer.

**Consequences.** No SDK, and one file defines each flag. A flagd outage costs flag freshness, not
requests (REL-2), logged once per outage. The service refuses to start if a flag it declares
isn't defined. Only boolean flags are supported so far.
