# ADR-0023: A request's log lines share state

- **Status:** Accepted
- **Date:** 2026-10-05 (8b47875, c3cd08b)

**Context.** Middleware hands inner handlers copies of the request. What an inner handler learned
(the route the router matched, the API key that authenticated) never reached the outer logging
middleware: the `route` field of every "response sent" line was empty once authentication was
added.

**Decision.** The correlation ID's context value is a small per-request state, shared by every
copy of the request, which inner handlers add to: `httpx.RecordRoute` records the route, and
authentication adds the key's name to every later log line. It costs the one allocation storing
the ID already did.

**Consequences.** Every line of a request carries its ID, route and key (OBS-2), with no change to
handler signatures. Anything else learned deep in a request can be logged the same way.
