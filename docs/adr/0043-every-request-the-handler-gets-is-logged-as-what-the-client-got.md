# ADR-0043: Every request the handler gets is logged as what the client got

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** "response sent" said what the handler wrote, which isn't always what the client got.
A response to a client that had gone, or whose write failed, was logged as sent. A panic after the
response had started got a 500 appended to the partial body, under the first status, and the log
said that status. A handler aborted with `http.ErrAbortHandler` had its `request received` and no
`response sent` at all. And a request still running when shutdown ran out of time
(`SHUTDOWN_TIMEOUT`) was cut off with the process, unanswered and unlogged.

**Decision.** "response sent" is logged for every request the handler gets, aborted ones too, and
adds `write_error` when the client didn't get the whole response: the request's context was
cancelled because the client left, a write failed, or the response was aborted. A panic after the
response has started drops the connection (`http.ErrAbortHandler`) instead of appending a 500, so
the client sees a failed response rather than a corrupt one. Its level stays the status's: a
client leaving isn't an incident, and a panic has its own `ERROR` line. When shutdown runs out of
time, the requests still running are stopped, not abandoned: every request's context derives from
one the server cancels with `ErrShuttingDown`, so what each waits on gives up and it answers 503,
`shutting_down`, with `Retry-After`, logged as `request stopped by shutdown`. They get 2 seconds;
only a handler that ignores its context is still running then, and it's cut off, with Serve's
error saying how many were.

**Consequences.** A `response sent` without `write_error` was delivered, as far as the server can
tell; one with it wasn't, and says why. Requests at shutdown get an answer they can retry. Since
`SHUTDOWN_TIMEOUT` is at least `REQUEST_TIMEOUT` (ADR-0041), a request that keeps its deadline
always finishes before shutdown gives up, so the stop is a backstop for one that doesn't. A
client that goes away after the response has left the server's buffer can't be detected, and
isn't flagged.
