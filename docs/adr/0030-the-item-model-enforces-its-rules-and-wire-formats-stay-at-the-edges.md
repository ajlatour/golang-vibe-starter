# ADR-0030: The item model enforces its rules, and wire formats stay at the edges

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** ADR-0015 deferred a domain-driven restructure until a real domain replaces items.
Reviewing the layout against domain-driven design found two flaws that don't wait for one. The
model was anemic: an item's rules (a name required and at most 200 characters, a description at
most 2000) were checked only by the HTTP handlers, so code that made an item any other way could
skip them. And the model carried JSON tags, so the API's wire format and the upstream's were both
the domain type's: a change to either reached into the model.

**Decision.** Take the part of the restructure that fixes those, and leave the layering. In
`internal/item`, an `ID`, a `Name` and a `Description` are value objects that can only be made
valid (`ParseID`, `NewName`, `NewDescription`), and a `Draft`, what a client asks to create or
replace, only from a valid name and description (`NewDraft`). The `Store` takes IDs and drafts, not
loose items. `Item` has no JSON shape: the handlers' `Request` and `Response` are the API's, and
`httpstore`'s own types are the upstream's, each translated at its edge. A store rebuilds the
items it holds with `Restore`, which doesn't re-check the rules: they govern what clients send,
and refusing an item the upstream sends that breaks them is a separate decision, not taken here.
This amends ADR-0015: the model and its edges change; the single package, without domain,
application and infrastructure layers, stands.

**Consequences.** The rules hold however an item is made, and an invalid ID can't reach a store or
an upstream URL by construction, not only by the handlers' check. The API's and the upstream's
JSON can change independently of the model and of each other. Every request translates between
shapes: listing items costs two more allocations a request (a slice each side). Tests make items
through the constructors (`itemtest.MustDraft`, `itemtest.MustID`, `item.Restore`). When a real
domain arrives, its rules have a place to live, and ADR-0015's layering is the next step if it
needs one.
