# ADR-0017: Item IDs are checked against an allowlist

- **Status:** Accepted
- **Date:** 2026-10-05 (f3a2197)

**Context.** IDs were path-escaped before going to the upstream, but `%2E%2E` reached it as
`/items/..`, which an upstream or proxy that normalizes paths, or decodes `%2F`, could route
elsewhere. Answering 400 or 404 for such an ID was considered.

**Decision.** An item ID is 1 to 128 letters, digits, `-` and `_` (`item.ValidID`). The handlers
answer any other with 404, as for an item that doesn't exist, without asking the store; the
store refuses one too.

**Consequences.** No path traversal into the upstream (API-4). The contract didn't change: an
impossible ID is just an unknown one. An upstream that issues other IDs would need the rule
widened, in the spec and the code together.
