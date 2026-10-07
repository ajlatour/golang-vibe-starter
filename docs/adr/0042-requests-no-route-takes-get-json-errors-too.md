# ADR-0042: Requests no route takes get JSON errors too

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** Every error the API's own code sends is JSON with a code (ADR-0033), but a request
that matched no route was answered by `http.ServeMux` itself, in plain text: `404 page not found`
for an unknown path, `Method Not Allowed` for a method its path has no route for. And `OPTIONS *`
never reached the handler at all: `net/http` answers it with an empty 200, unlogged.

**Decision.** The router gets a catch-all route, `httpx.HandleUnmatched`, registered last. It
answers 404 `not_found` for a path no route has, or 405 `method_not_allowed` with `Allow` when the
path has routes for other methods, which it works out by asking the router about each method only
when nothing matched. A target that isn't a path (`OPTIONS *`, `CONNECT`'s) is answered 400
`invalid_target` before routing (`httpx.RejectInvalidTarget`), and the server passes `OPTIONS *` on
instead of answering it (`DisableGeneralOptionsHandler`). These requests are logged with an empty
`route`, as before, and come after authentication and the limits, like any request.

**Consequences.** A client can treat every error from the API the same way. Two codes are new,
`method_not_allowed` and `invalid_target`, and `not_found` now also means a path nothing serves. A
matched route costs nothing more. A route added later must be registered before the catch-all,
which `HandleUnmatched`'s doc says, and the router can't have a `/` route of its own.
