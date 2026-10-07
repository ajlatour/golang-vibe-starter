# ADR-0040: Items have ETags, and writes can be conditional

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** A `PUT` replaced an item whatever it had become since the client read it, so two
clients editing the same item would each overwrite the other's change without knowing: a lost
update. HTTP's conditional requests are the standard answer.

**Decision.** Every item response (`GET`, `POST` and `PUT` on an item) carries an `ETag`: an
FNV-1a hash of the item's name, description and last update time, so it changes with any change,
even two in one tick of the clock. A `PUT` or `DELETE` with `If-Match` goes ahead only if the item
still has a listed tag (compared strongly, so a weak tag never matches), or for `*`, exists at
all; otherwise it's a 412 with `precondition_failed`, and nothing changes. A `GET` with a matching
`If-None-Match` (compared weakly) is a 304 with no body. Without the headers, requests behave as
before. The check is in the handlers: they read the item, compare, then write.

**Consequences.** Clients that send `If-Match` can't lose each other's updates in the common case,
a client acting on a stale copy. Two writers racing between the check and the write can still both
pass; closing that needs the upstream to take `If-Match` itself, so the comparison and the write
are one step, which the contract can grow when there's a real upstream. A conditional write costs
one more upstream read. The tag is the API's own, not the upstream's.
