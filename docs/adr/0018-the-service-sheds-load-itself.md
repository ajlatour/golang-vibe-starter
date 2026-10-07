# ADR-0018: The service sheds load itself

- **Status:** Accepted
- **Date:** 2026-10-05 (810139f)

**Context.** Every request fanned out to the upstream, so a flood passed straight through. Limits
in a gateway would protect the service only where there is one.

**Decision.** `internal/limit` caps requests in flight (503) before authentication, and limits each
client with a token bucket (429) after it. A client is its API key, or its address without one.
Both refuse at once with `Retry-After` rather than queue. The probes are exempt.

**Consequences.** The upstream sees at most the service's capacity (REL-3). Limits are per
instance. Behind a proxy, keyless clients share the proxy's address, since proxy headers are never
trusted (ADR-0012). The local stack turns the per-client limit off so load tests measure the
service.
