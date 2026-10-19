# Tracing

The service records a span for each request it serves, and for each call it makes on the way (the
upstream, the request-validation service, flagd), and exports them over OTLP/HTTP, in OTLP's JSON
encoding, to an OpenTelemetry Collector or a backend that takes it, such as Jaeger. One that takes
only protobuf goes behind a Collector, which translates. It's off unless
there's somewhere to send them, and it's optional in the code too: the OpenTelemetry SDK is in one
package, which a service that will never trace can delete ([removing it](#removing-it)).

- [Turning it on](#turning-it-on)
- [What's recorded](#whats-recorded)
- [Seeing traces locally](#seeing-traces-locally)
- [Traces and logs](#traces-and-logs)
- [What it costs](#what-it-costs)
- [Removing it](#removing-it)

## Turning it on

Set where to export spans, over OTLP/HTTP:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318    # spans go to /v1/traces there
```

Startup then logs `using tracing`. The rest of the settings are OpenTelemetry's usual ones, from
the environment:

| Variable | What it sets |
| -------- | ------------ |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | The full URL for traces, instead of the endpoint above. |
| `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_TRACES_HEADERS` | Headers for the export, such as a backend's API key: `x-api-key=...`, values URL-encoded. |
| `OTEL_EXPORTER_OTLP_TIMEOUT`, `OTEL_EXPORTER_OTLP_TRACES_TIMEOUT` | How long an export may take, in milliseconds: `10000` by default. |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | How much to keep. The default, `parentbased_always_on`, keeps every trace a caller didn't decide against; `parentbased_traceidratio` with `0.1` keeps a tenth of the service's own. |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | The service's name in its traces (by default, the repository's), and attributes such as `deployment.environment=production`. |

Spans are exported in batches, every 5 seconds, and the last at shutdown. One that can't be
exported is dropped, and logged as `tracing failed`; a request never waits on the export.

## What's recorded

- **Each request** (`internal/httpx`, a server span): named for its route, such as
  `GET /items/{id}`, with its method, route and status. A 5xx is an error. A request with a
  `traceparent` is a child of the caller's span, in the caller's trace.
- **Each call it makes** (`internal/jsoncall`, a client span, the request's child): named for the
  call, such as `upstream GET /items/widget`, with its method and status, or why it failed. A
  4xx or 5xx is an error. The call sends its span's `traceparent` on, so the service it calls
  continues the trace.

The probes, `/healthz` and `/readyz`, record none, since they're asked every few seconds; nor do
readiness checks and flagd's background refreshes, which are outside any request.

## Seeing traces locally

```sh
make tracing                                # the stack, with Jaeger, the API sending it spans
scripts/api GET /items/widget
open http://localhost:16686                 # Jaeger: search for the service golang-vibe-starter
make down
```

Each request is a trace: the API's span for it, and inside it a span for each call, here to
WireMock (the upstream and the request-validation service) and flagd. `make tracing` is `make up`
with Jaeger, and `OTEL_EXPORTER_OTLP_ENDPOINT` set for the API; Jaeger keeps traces in memory
until it stops. For an API run with `go run` (`make dev`, `make run`), start Jaeger alone and point
the API at it:

```sh
docker compose --profile tracing up -d jaeger
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 make dev
```

## Traces and logs

Every log line of a request has its `trace_id`, the trace its spans are in, whether or not they're
recorded. So from a slow span, its trace ID finds the request's log lines; and from a log line, its
trace ID finds the trace. A request that arrives without a `traceparent` starts a trace, and its
spans keep that trace's ID.

## What it costs

Off, nothing: the SDK isn't started, and the request path is as it was. The binary is about 1 MB
bigger for the SDK being in it. On, a request makes a
span, and one per call, which the perf tests don't measure, since they run with tracing off.
Sampling less (`OTEL_TRACES_SAMPLER`) cuts what's exported, not what's made.

## Removing it

All of OpenTelemetry is in `internal/telemetry` and
`cmd/api/telemetry.go` ([ADR-0046](adr/0046-the-opentelemetry-sdk-is-allowed-in-one-optional-adapter.md)).
To take it out of a service:

1. Delete both, and in `cmd/api/main.go` the call to `newSpans`.
2. In `cmd/api/config.go`, delete `TracesEndpoint`.
3. Run `go mod tidy`: `go.mod` has no requirements again.
4. Run `make check`: the drift tests name what still describes it (the README's settings,
   `.env.example`, the log contract, the package table and the adapters in
   `docs/architecture_test.go`). Delete each, and this page.

The span hooks stay (`tracectx.Spans`, `httpx.WithSpans`): they're the standard library, and do
nothing without a recorder.
