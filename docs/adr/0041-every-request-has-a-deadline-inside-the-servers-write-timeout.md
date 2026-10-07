# ADR-0041: Every request has a deadline, inside the server's write timeout

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** The server gave every response 15 seconds to be written (`WriteTimeout`), but
nothing kept a request within that. Each upstream call had its own timeout, and a request can make
several: a conditional write reads, then writes, and a GET that failed slowly was retried. A
request that ran past 15 seconds had its connection cut, so the client got nothing, while the
handler carried on, answered into the void, and logged "response sent" with a status nobody
received.

**Decision.** Every request gets a deadline, `REQUEST_TIMEOUT` (10s by default), as a context
deadline set inside the standard middleware: when it passes, the calls the request is waiting on
give up, and the handler answers, usually 504, which is logged like any response. The server's
write timeout is `REQUEST_TIMEOUT` plus 5 seconds, so that answer always gets out. The service
refuses to start with an `UPSTREAM_TIMEOUT`, `VALIDATOR_TIMEOUT` or `FLAGS_TIMEOUT` (when those
services are used) that isn't shorter than `REQUEST_TIMEOUT`, since the deadline would always
fire first, or with a `SHUTDOWN_TIMEOUT` shorter than it, which would cut off requests in flight
at shutdown. An upstream GET is retried only after a fast failure (within a second, as the retry
was always meant to be) and only while a whole attempt still fits before the deadline.

**Consequences.** Every request that reaches the handler is answered within `REQUEST_TIMEOUT` plus
the time to write it, and its log line tells the truth. A deployment with a long `UPSTREAM_TIMEOUT`
or a short `SHUTDOWN_TIMEOUT` must now set `REQUEST_TIMEOUT` (or those) to match: it fails at
startup with a message naming both, rather than misbehaving later. The deadline costs each request
a context and a timer. The deadline isn't passed on to the upstream (a header such as
`Request-Timeout`): each call is bounded by both its own timeout and the request's, which is
enough while there's one upstream hop.
