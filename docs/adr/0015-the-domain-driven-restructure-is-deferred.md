# ADR-0015: The domain-driven restructure is deferred

- **Status:** Accepted
- **Date:** 2026-10-05 (pull request #1, closed)

**Context.** A restructure along domain-driven lines was built: a pure domain package, a name value
object with its invariants, HTTP handlers and DTOs in their own package, and the store's wire
types as an anti-corruption layer. Items are a placeholder with CRUD rules only.

**Decision.** Don't adopt it now. Keep `internal/item` as model, store interface and handlers
together, until a real domain with real rules replaces items.

**Consequences.** Less structure than a growing domain would need. When one arrives, pull request
#1 shows a worked restructure to start from; its branch was deleted, and GitHub can restore it.
