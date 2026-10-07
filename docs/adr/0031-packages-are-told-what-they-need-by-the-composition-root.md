# ADR-0031: Packages are told what they need, by the composition root

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** A review of coupling found three packages reaching into others for something they
could be told. `httpstore`, the upstream adapter, chose the API's 502 and 504 itself, by
implementing `httpx.StatusError`: the adapter knew how the API answers its clients. The rate limiter
read `apikey`'s request context to learn who a client was and its limit, so changing
authentication meant changing the limiter. And `item` declared its flag in `featureflag`'s
package-level registry, filled as packages initialised, which `cmd/api` then read: global state
shared by every test, and a dependency for the sake of one string.

**Decision.** Each package states what it knows, and `cmd/api`, the composition root, connects
them. `httpstore`'s errors say what went wrong, matching `item.ErrStoreTimeout` or
`item.ErrStoreUnavailable`, and the item handlers decide those are 504 and 502. The limiter takes an
`Identify` function (who is this client, and what's their own limit?) and knows nothing of how it
answers; `cmd/api` passes one built from the API keys. `item` lists the flags its handlers read
(`FlagKeys`), and `cmd/api` checks them against the definitions; the registry is gone. The package
table in `docs/architecture.md` now fails on an import it allows that a package doesn't make, so it
reads as the dependencies there are.

**Consequences.** `httpstore` no longer imports `httpx`, `limit` no longer imports `apikey`, and
`item` no longer imports `featureflag`. Another interface over the store, another way to
authenticate, or another package with flags plugs in at `cmd/api` without touching them. The cost
is explicitness: `cmd/api` has a little more wiring, and a flag must be listed as well as named,
which a test in `internal/item` checks.
