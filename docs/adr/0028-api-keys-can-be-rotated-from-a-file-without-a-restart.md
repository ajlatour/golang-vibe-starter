# ADR-0028: API keys can be rotated from a file, without a restart

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** ADR-0016 read `API_KEYS` once, so rotating a key, or revoking a leaked one, meant a
restart. It also gave every key the same rate limit, though a batch job and an interactive client
need different ones. Watching a file for changes, with fsnotify, would be a dependency
(ADR-0002).

**Decision.** `API_KEYS_FILE` names a file of the same entries, one per line, which the service
checks every `API_KEYS_RELOAD` (10s) by polling and swaps in atomically once a change has read the
same on two checks in a row, so a file caught half-written is never used. A file
that can't be read or parsed, or holds no keys, changes nothing and is logged once, so a bad edit
can't lock every client out. An entry may add a fourth field, the key's own rate limit, which
replaces `RATE_LIMIT` for it and applies even when that's off. This amends ADR-0016's consequence
that rotation needs a restart; the rest of it stands.

**Consequences.** Keys rotate with no downtime: add the new key, move clients to it, remove the
old one (SEC-10). Polling suits a mounted secret, which is swapped rather than edited. A change
takes up to twice `API_KEYS_RELOAD` to apply. `API_KEYS` and `API_KEYS_FILE` can't both be set.
