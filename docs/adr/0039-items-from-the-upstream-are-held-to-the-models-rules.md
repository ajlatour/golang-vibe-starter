# ADR-0039: Items from the upstream are held to the model's rules

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** The model holds what clients send to its rules (ADR-0030), but items the upstream sent
back were restored as they came, unchecked: a decision deferred then. An upstream with a bug, or a
compromised one, could send an item the API couldn't have made: an ID such as `../admin`, which
goes into `Location` headers and later URLs, a name over the limit the API's own contract
promises, or no timestamps. The API would pass it on, breaking its contract with its clients.

**Decision.** `httpstore` builds every item the upstream sends with `item.Parse`, which holds it to
the rules a client's input meets: a valid ID, a name and description within their limits (the name
trimmed), and both timestamps. An item that breaks one makes the call an unusable answer, like a
body that won't decode: logged with which rule (never the value, which came from outside), a 502
to the client, and not retried, since the same request would get the same item. One bad item fails
its whole page. `item.Restore` stays for stores whose items can only have come through the model.

**Consequences.** The API's responses keep its contract whatever the upstream does. An upstream
that sends bad data is visible at once, as 502s and their log lines, rather than as bad data
passed on. A single bad item makes its page unreadable until the upstream is fixed: strict, but
the alternative, skipping it, would hide data silently.
