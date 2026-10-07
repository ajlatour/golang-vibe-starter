# ADR-0032: Every request can be checked against a validation service, failing closed

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** An API may not be able to serve a request until another service approves it: a fraud
or entitlement check, a policy engine, token introspection. Such a call sits on every request's
path, so its latency is every request's, and its outages are every request's unless the API is
careful.

**Decision.** `internal/validator` checks every request but the probes against the service at
`VALIDATOR_URL`, as a middleware after authentication and the per-client rate limit: it knows
who's calling, and a client's flood never reaches the service. The call goes through `jsoncall`,
like every outbound call (ADR-0007, ADR-0019), with its own connection pool and a short timeout
(`VALIDATOR_TIMEOUT`, 300ms). A refusal is a 403 whose reason is logged, never sent. When the
service can't answer, the API fails closed by default (503 with `Retry-After`), or open with
`VALIDATOR_FAILURE_MODE=open`; either way it asks again at most once a second, answering the
requests in between at once, and logs an outage once each way, as for flagd (ADR-0029). Failing
closed, `/readyz` also requires the service to be reachable (ADR-0021). The request and response
shapes are a placeholder for the real service's. Whether answers are acted on is the
`request-validation-enforce` feature flag, off by default: shadow mode asks the service and logs
what it would have done, refusing nothing, so the check can be deployed dark and switched on, or
back off, at runtime; a flagd outage falls back to shadow, never to refusing. The API measures the
calls (`validator_call_duration_seconds`, `validator_calls_total{outcome}`), and the performance
report and `main`'s trends track them.

**Consequences.** Every request costs a call to the validation service, shadow mode included, by
design: the dark phase measures what enforcing will cost. A cache can come when the real service's
rules say what can be cached. A validation outage, failing closed, is an
outage of the API, by design: the check isn't skipped quietly. Without `VALIDATOR_URL` nothing is
checked, logged as a warning at startup. The local stack runs it against a WireMock stub, so the
load tests include its cost.
