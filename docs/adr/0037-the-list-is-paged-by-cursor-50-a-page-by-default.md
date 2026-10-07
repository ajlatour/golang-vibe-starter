# ADR-0037: The list is paged by cursor, 50 a page by default

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** `GET /items` returned every item in one response, as much as the upstream sent (up to
4 MiB). That grows with the data, and can't be bounded later without breaking the clients that
rely on getting everything, so it's cheapest to decide before there are any.

**Decision.** The list comes a page at a time, by cursor: `?limit` (1 to 200, 50 without it) and
`?cursor`, with `next_cursor` in the response until the last page. A cursor marks the place after a
page's last item, not an offset, so pages stay right while items are added or deleted: nothing is
skipped or listed twice. Cursors are opaque, made by the store: the in-memory one encodes the last
item's creation time and ID; `httpstore` passes the API's page through to the upstream, which pages
the same way, and its cursors back. A client's cursor is checked for form (1 to 256 URL-safe
characters) before it reaches an upstream URL; a `limit` out of range, or a cursor the store
didn't give out (the upstream's 400), is a 400, `invalid_parameter`. The `total` the
`items-list-total` flag adds counts the page. A list's query string, the cursor included, is never
logged.

**Consequences.** Every response is bounded, by default. A client that ignored paging now gets the
first 50 items, not all of them: a behaviour change, made while there are no clients to break.
Paging means more requests for a client that wants everything, and the upstream must page too.
