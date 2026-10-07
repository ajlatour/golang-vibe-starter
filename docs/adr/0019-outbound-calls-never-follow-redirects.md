# ADR-0019: Outbound calls never follow redirects

- **Status:** Accepted
- **Date:** 2026-10-05 (463a27f)

**Context.** Go's HTTP client follows redirects by default. A compromised or misconfigured
upstream could send the service to any address it can reach, internal ones included, with the
request body on a 307 or 308.

**Decision.** `jsoncall` never follows a redirect, whatever client it's given: a 3xx is an
unexpected status, so a 502. It keeps one no-redirect copy of each client, so this costs nothing
per call.

**Consequences.** No server-side request forgery through the upstream (SEC-5). An upstream that
moves must be reconfigured, not followed.
