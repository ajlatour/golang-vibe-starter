# ADR-0029: Flag values are cached, and a flagd outage is retried once per TTL

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** ADR-0005 evaluated every flag by asking flagd, on every request that reads it: a
network round trip on the request path, and load on flagd that grows with the API's traffic. Worse,
a flagd that stops answering without refusing connections made every such request wait out
`FLAGS_TIMEOUT` (500ms) before taking the default, so an outage that costs no requests still costs
every one of them half a second. The OpenFeature SDK's flagd provider avoids both by streaming
flag changes over gRPC, which would be a dependency (ADR-0002).

**Decision.** The OFREP client keeps each value flagd serves for `FLAGS_CACHE_TTL` (1s). Only
values flagd served are kept: a failed evaluation is asked again next time. While flagd isn't
answering, it is asked at most once per TTL, by whichever evaluation comes first, and every
evaluation in between takes its default at once, counted in the outage's `fallbacks`. `0s` turns
both off and asks flagd every time, as before. This amends ADR-0005's consequence that a flagd
outage costs only flag freshness; it now costs a request nothing but the default either.

**Consequences.** A flag change takes up to the TTL to reach the API, and instances may disagree
for that long, which a second makes harmless for a feature toggle but would matter for a kill
switch that must act at once (set the TTL lower, or to `0s`). flagd sees about one request per flag
per TTL per instance, whatever the traffic. An outage costs one `FLAGS_TIMEOUT` per TTL instead of
one per request. The integration tests run with the cache off, so a flag step's change applies to
the next request.
