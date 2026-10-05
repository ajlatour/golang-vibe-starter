# Calling the API

Two ways to call the API by hand: `scripts/api`, a small wrapper around curl that fills in what
every call needs, or plain curl. Both need only curl and bash. Start the stack first with
`make up` (or `make run`); the API listens on `localhost:8080`.

Every command on this page is run by a test, `cmd/api/curl_test.go`, against the real handlers,
and the comment under each one is the status it must get. Against the local stack the commands
get the same statuses. The stack's upstream is WireMock, which keeps no state: it always serves
`widget`, `gadget` and `gizmo`, and a deleted item comes back. See
[WireMock stubs](local-stack.md#wiremock-stubs).

## scripts/api

```
scripts/api METHOD PATH [BODY] [curl options...]
```

`BODY` is JSON, `@file` to read it from a file, or `-` to read it from stdin. It is sent with
`Content-Type: application/json`, which the API requires of every request with a body. Anything
after the body goes to curl unchanged: `-v` to see the whole exchange, `-H` to add a header.
`scripts/api --help` prints the details.

The status line, the `Client-Correlation-Id` and any `Location` header go to stderr, and the body
to stdout. On a terminal the body is pretty-printed if [jq](https://jqlang.org) is installed.
When the output is piped or redirected it is exactly what the API sent, so
`scripts/api GET /items | jq '.items[].id'` works. The exit status is 0 for a success, 1 for a 4xx
or 5xx response (the body is still printed), and curl's own status if the API didn't answer.

### Reading

```sh
scripts/api GET /items
# 200 OK

scripts/api GET /items/widget
# 200 OK

scripts/api GET /healthz
# 200 OK
```

For example:

```text
$ scripts/api GET /items/widget
HTTP/1.1 200 OK
Client-Correlation-Id: 4LSBQEPNJ5NMWEBHAE7IJOPYIX
{
  "id": "widget",
  "name": "Widget",
  "description": "A standard widget.",
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}
```

### Creating, replacing and deleting

```sh
scripts/api POST /items '{"name": "Sprocket", "description": "Spins."}'
# 201 Created

scripts/api PUT /items/widget '{"name": "Widget", "description": "Now in blue."}'
# 200 OK

scripts/api DELETE /items/gizmo
# 204 No Content

echo '{"name": "From stdin"}' | scripts/api POST /items -
# 201 Created
```

A create answers with a `Location` header naming the new item. A replace sets both `name` and
`description`: leaving `description` out clears it.

### Errors

```sh
scripts/api GET /items/no-such-item
# 404 Not Found

scripts/api POST /items '{"name": "   "}'
# 422 Unprocessable Entity

scripts/api POST /items '{"name": "Sprocket", "colour": "red"}'
# 400 Bad Request

scripts/api POST /items '{"name": '
# 400 Bad Request
```

Every error body is `{"error": "..."}`, saying what was wrong. The README lists
[every status](../README.md#api). To see the 502 and 504 the API gives when its upstream fails,
switch on a WireMock fault: `make fault FAULT=unavailable` for a 502, or `FAULT=timeout-list` for a
504 from `GET /items`, and `make fault-reset` to switch it off again. See
[Fault injection](local-stack.md#fault-injection).

### Correlation IDs

Every response carries a `Client-Correlation-Id`, and the API's log lines for that request carry
the same ID. Quote it when reporting a problem. To choose it yourself, so you can find the call
in the logs (`make logs`), set `CORRELATION_ID`:

```sh
CORRELATION_ID=debug-widget-1 scripts/api GET /items/widget
# 200 OK
```

An ID is 1 to 128 letters, digits and `-_.:`. The API replaces anything else with one of its
own.

### Another environment

`API_URL` points the script at another deployment of the API, for one call
(`API_URL=https://items.staging.example.com scripts/api GET /items`) or, exported, for the rest of
the shell session.

## Plain curl

The same calls with curl alone. `-i` prints the status line and headers before the body.

```sh
curl -i localhost:8080/items
# 200 OK

curl -i localhost:8080/items/widget
# 200 OK

curl -i localhost:8080/items -H 'Content-Type: application/json' \
  -d '{"name": "Sprocket", "description": "Spins."}'
# 201 Created

curl -i -X PUT localhost:8080/items/widget -H 'Content-Type: application/json' \
  -d '{"name": "Widget", "description": "Now in blue."}'
# 200 OK

curl -i -X DELETE localhost:8080/items/gadget
# 204 No Content

curl -i localhost:8080/items -H 'Client-Correlation-Id: debug-list-1'
# 200 OK
```

Two mistakes the API refuses:

```sh
# curl -d sends Content-Type: application/x-www-form-urlencoded unless told otherwise.
curl -i localhost:8080/items -d '{"name": "Sprocket"}'
# 415 Unsupported Media Type

# Content-Type, Authorization and Client-Correlation-Id may each appear only once.
curl -i localhost:8080/items -H 'Client-Correlation-Id: a' -H 'Client-Correlation-Id: b'
# 400 Bad Request
```

The README's [Request headers](../README.md#request-headers) section has every header rule, and
[`docs/openapi.json`](openapi.json) the full contract.
