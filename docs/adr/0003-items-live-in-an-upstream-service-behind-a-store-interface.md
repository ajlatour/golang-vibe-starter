# ADR-0003: Items live in an upstream service, behind a store interface

- **Status:** Accepted
- **Date:** 2026-10-03 (8012ea7)

**Context.** The service fronts data owned by another system. Its handlers shouldn't depend on
how that system is reached, and tests shouldn't need it running.

**Decision.** The handlers depend on `item.Store`. The production store, `httpstore`, calls the
upstream's `/items` routes over HTTP. Tests use an in-memory store, and every store must pass the
same contract (`itemtest.StoreContract`).

**Consequences.** The upstream can change, or be replaced, by changing `UPSTREAM_URL` or adding a
store. The service owns no data and no consistency. Upstream failures are the upstream's: 502 or
504, never 500 (REL-1).
