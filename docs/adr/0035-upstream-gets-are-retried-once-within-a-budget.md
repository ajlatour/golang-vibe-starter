# ADR-0035: Upstream GETs are retried once, within a budget

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** A call to the upstream can fail for a moment: a connection dropped by a load
balancer, an instance restarting. Each such blip became a 502 for the client, though the same
request a moment later would have worked. Retrying blindly is worse, though: it repeats writes,
doubles the wait after a timeout, and when the upstream is really down, multiplies its load just
when it can least take it.

**Decision.** `httpstore` tries a GET again, up to `UPSTREAM_RETRIES` times (1 by default, at most
3), when the upstream gave no usable answer fast: a refused or reset connection, or a 5xx. Never a
write, never a timeout, never a 4xx (an answer, which a retry would only repeat), and never once the
client has gone. Retries pause briefly, jittered (25 to 50ms), and share a token-bucket budget:
each request earns a tenth of a retry, up to ten saved, so retries add at most about 10% to the
upstream's traffic however many requests fail. Each retry is logged, `upstream request retried`.

**Consequences.** A blip costs a client a few tens of milliseconds instead of a 502. A down upstream
sees about 10% more traffic, not double, and clients get their 502s as before once the budget is
spent. Writes still fail on the first error, and so stay the caller's to retry, knowing whether the
write is safe to repeat.
