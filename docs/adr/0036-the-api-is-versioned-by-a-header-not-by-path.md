# ADR-0036: The API is versioned by a header, not by path

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** The API is at 0.x, with no external clients yet. Once it has them, a breaking change
needs a way to serve the old behaviour beside the new; adding that way later is itself a breaking
change, so it's cheapest to decide now. The usual choices are a path prefix (`/v1/items`) or a
header.

**Decision.** A header, `API-Version`. A request may send it; without it, it gets the current
version. Every response carries the version that answered, set by the standard middleware so the
responses refused early carry it too. A version the API doesn't serve is a 400 with
`unsupported_api_version`, checked after header screening and before authentication; the probes
answer any. `API-Version` may appear only once. The versions are one list,
`httpx.APIVersions`, today `1`. The request isn't marked with its version, which would cost an
allocation a request while there's one: a handler that comes to differ between versions reads the
header.

**Consequences.** URLs never change with a version, so links, routes and load-balancer rules stay
as they are. A version is less visible than in a path: it's in a header, not in logs of URLs or in
links. A breaking change adds a version to the list and serves both; clients that sent the header
keep what they were written against, and those that didn't move to the new one, so clients that
care should send it.
