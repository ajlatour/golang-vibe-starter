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
| refuses | 403 `{"error": "the request was refused"}` | `request refused by validation`, with the service's `reason`, which never reaches the client |
| can't answer: unreachable, timed out (`VALIDATOR_TIMEOUT`, 300ms), an error status, a body that won't decode | failing closed (the default): 503, `Retry-After: 1`; failing open (`VALIDATOR_FAILURE_MODE=open`): the response, unchecked | `request validation unavailable` once when it starts, `request validation recovered` with the count when it ends |

To adapt it to a real service, change `validator.Request` to carry what that service needs, and
`Client.Check` to speak its API. The middleware, the failure handling and the logging stay as they
are, and so do their tests.

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
scripts/api GET /items/refused-by-validator   # 403
make fault FAULT=validator-down               # the service fails every call with 503
scripts/api GET /items                        # 503, Retry-After: 1
make fault-reset
```

`integration/features/request-validation.feature` checks the same against the stack.
