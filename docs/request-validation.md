# Request validation

Some APIs can't serve a request until another service has approved it: a fraud or entitlement
check, a policy engine, token introspection. `internal/validator` is a scaffold for that: with
`VALIDATOR_URL` set, every request but the probes is checked against that service before the
routes see it ([ADR-0032](decisions.md#adr-0032-every-request-can-be-checked-against-a-validation-service-failing-closed)).

## Where it sits

After authentication and the per-client rate limit, before routing
([the request path](architecture.md#the-request-path)): it knows who's calling (the API key's
name), and a client's flood is refused with 429 before it can reach the validation service.
`/healthz` and `/readyz` are never checked.

## The contract

A placeholder, to replace with your service's. The API sends:

```http
POST {VALIDATOR_URL}/validate
Content-Type: application/json

{"client": "ci", "method": "GET", "path": "/items/widget"}
```

`client` is the API key's name (empty without one). Never the request's credentials or body. The
service answers `200` with a decision:

```json
{"allow": true}
{"allow": false, "reason": "over quota"}
```

| The service | The client gets | Logged |
| ----------- | --------------- | ------ |
| allows | the response, as without the check | nothing extra |
| refuses | enforcing: 403 `{"error": "the request was refused"}`; in shadow mode (below), the response, as if allowed | `request refused by validation`, with the service's `reason`, which never reaches the client |
| can't answer: unreachable, timed out (`VALIDATOR_TIMEOUT`, 300ms), an error status, a body that won't decode | enforcing and failing closed (the default): 503, `Retry-After: 1`; failing open (`VALIDATOR_FAILURE_MODE=open`) or in shadow mode: the response, unchecked | `request validation unavailable` once when it starts, `request validation recovered` with the count when it ends |

To adapt it to a real service, change `validator.Request` to carry what that service needs, and
`Client.Check` to speak its API. The middleware, the failure handling and the logging stay as they
are, and so do their tests.

## Deploying it dark

The `request-validation-enforce` feature flag decides whether the service's answers are acted on.
It's off by default: **shadow mode**. The service is asked about every request, as it will be
when enforcing, but every request goes through. A refusal is logged as `request would be refused
by validation`, with the reason, and an outage is logged without failing anything. So before
anything is enforced, the logs and metrics say how often it would refuse and why, whether it's
reachable from production, and what it costs ([below](#what-it-costs)).

| `VALIDATOR_URL` | The flag | What happens |
| --------------- | -------- | ------------ |
| unset | either | nothing is checked; a warning at startup |
| set | off (default) | shadow: checked and logged, never refused |
| set | on | enforced: refusals are 403s, and outages 503s (or let through, failing open) |

Turning the flag on in flagd enforces it on every instance within a second (`FLAGS_CACHE_TTL`), with
no deploy, and turning it off rolls back as fast. If flagd itself can't answer, the flag takes its
default, off, so a flagd outage can only ever put the check back into shadow mode, never start
refusing requests. `/readyz` waits on the validation service only while it's enforced and failing
closed.

## What it costs

The check is a network call on every request's path, so the API measures it. `/metrics`
(`PPROF_ADDR`) serves:

- `validator_call_duration_seconds`, a histogram of the calls made;
- `validator_calls_total{outcome}`, each check's outcome: `allowed`, `refused`, `shadow_refused`,
  `unavailable`, or `not_asked` (during an outage, between tries).

The local stack runs the check against WireMock, so the load tests include its cost. Each pull
request's performance report has a **Request validation** section with the calls' median and p99,
from the API's own histogram, next to `main`'s; and `main`'s trends, which every release attaches
in its Performance history, chart them over time ([Performance](performance.md#trends)).

## When it can't answer

The check is on every request's path, so an outage of the validation service must not make every
request wait out the timeout. Once a call fails, the service is asked again at most once a second:
every request in between gets the failure mode at once, a 503 or straight through. The first call
that gets an answer ends the outage.

Failing closed is the default because a check the API can't serve without shouldn't be skipped
quietly. Then `/readyz` also checks the validation service can be reached (any HTTP answer
counts, as for the upstream: [ADR-0021](decisions.md#adr-0021-readiness-means-the-upstream-can-be-reached)),
so an instance that can't reach it is taken out of rotation. Failing open, it isn't: the instance
can still serve.

Without `VALIDATOR_URL` nothing is checked, and the service logs a warning at startup, so a
production deployment missing it shows in its first lines.

## Locally

`make up` runs it against WireMock's stub (`wiremock/mappings/validator.json`), which allows every
request but `/items/refused-by-validator`:

```sh
scripts/api GET /items/refused-by-validator   # 404: shadow mode (the flag's default) lets it through
# turn it on: in flagd/flags.json set request-validation-enforce's "defaultVariant" to "on"
scripts/api GET /items/refused-by-validator   # 403
make fault FAULT=validator-down               # the service fails every call with 503
scripts/api GET /items                        # 503, Retry-After: 1
make fault-reset
```

`integration/features/request-validation.feature` checks the same against the stack.
