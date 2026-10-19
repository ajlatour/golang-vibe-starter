### Added

- `tracectx.Spans`, given to `httpx.WithSpans`, is told of every request's span and of each call it
  makes (`internal/jsoncall`), with its trace context, status and route, and gives back the span's
  context, which the calls then carry on. Nothing in the service sets one yet: it's the hook an
  OpenTelemetry SDK adapter plugs into. Without one, nothing changes.

### Changed

- A trace the service starts itself, for a request with no `traceparent`, no longer has a span ID
  that stands for nothing; each call it makes onward still gets one of its own.
