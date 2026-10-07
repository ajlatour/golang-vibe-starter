# ADR-0012: Request headers are screened before the handlers

- **Status:** Accepted
- **Date:** 2026-10-04 (9d94fb6)

**Context.** Headers are untrusted input that every handler would otherwise have to defend
against: oversized headers, duplicated single-use headers, bodies that aren't JSON, and proxy or
method-override headers that could spoof a client's address or method.

**Decision.** `internal/headerguard` screens every request first: limits on header count and size
(431), single-use headers sent twice (400), bodies not sent as JSON (415), and proxy and override
headers removed. It can report instead of enforce, for rolling out.

**Consequences.** Handlers can trust what reaches them (SEC-4). Requiring JSON bodies also blocks
cross-site form posts. Nothing in the service may trust `X-Forwarded-For`, which matters for rate
limiting behind a proxy (ADR-0018). The limits are configurable; which headers are single-use or
removed is not.
