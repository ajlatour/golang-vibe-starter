# ADR-0033: Errors carry a stable code, from one list

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** Every error was `{"error": "..."}`, a message for people. A client acting on errors
had only the status, and some statuses mean several things: a 403 for a read-only key or for a
refusal by the validation service (ADR-0032), a 503 for a busy service or an unavailable
validation service. Telling them apart meant matching message text, which then couldn't be
reworded.

**Decision.** Every error body also carries `code`: `{"error": "...", "code": "rate_limited"}`.
The codes are one list, `httpx.Code` in `internal/httpx/codes.go`, and `httpx.WriteError` takes
one, so no error can be written without it. The OpenAPI spec's Error schema lists them as an enum,
which tests hold to the list both ways, and every scenario's response is checked against it; the
README's table of statuses lists them too, also tested. "response sent" logs the code as
`response_code`. The change is additive: `error` is unchanged, so no client breaks. A format such
as RFC 9457's `application/problem+json` was not adopted: it changes the body's shape, which only
clients asking for it would justify.

**Consequences.** Clients switch on `code`, and messages can be reworded freely. A new kind of
error needs a code added to the list, the spec and the README, which the tests enforce. Logs and
dashboards can group errors by meaning.
