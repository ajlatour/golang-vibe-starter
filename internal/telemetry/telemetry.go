// Package telemetry records the service's spans with the OpenTelemetry SDK,
// and exports them over OTLP/HTTP, as JSON (export.go): a tracectx.Spans,
// given to httpx.WithSpans. It's the only package that imports OpenTelemetry
// (ADR-0046), and cmd/api uses it only when OTEL_EXPORTER_OTLP_ENDPOINT is
// set, so the service runs without it, and removing it is deleting this
// package and cmd/api/telemetry.go (docs/tracing.md#removing-it).
//
// Its settings are OpenTelemetry's, from the environment, but for where to
// export, which cmd/api reads and New is told: the export's headers and
// timeout (OTEL_EXPORTER_OTLP_HEADERS, OTEL_EXPORTER_OTLP_TIMEOUT), how much
// to sample (OTEL_TRACES_SAMPLER, OTEL_TRACES_SAMPLER_ARG), and the service's
// name and resource (OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES).
package telemetry

import (
	"context"
	"crypto/rand"
	"fmt"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

// Spans records spans, and exports them in batches. Shutdown exports what's
// left.
type Spans struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
}

var _ tracectx.Spans = (*Spans)(nil)

// New returns Spans exporting over OTLP/HTTP to endpoint, the full URL
// (such as http://collector:4318/v1/traces), as the environment configures
// the rest, from a service called service (unless OTEL_SERVICE_NAME says
// otherwise) at version. onError is told of what goes wrong in the SDK, an
// export that fails above all, instead of the SDK writing it to stderr, out
// of the service's log format; it's the SDK's error handler, process-wide.
// The SDK drops spans it can't export, and never blocks a request.
func New(ctx context.Context, endpoint, service, version string, onError func(error)) (*Spans, error) {
	otel.SetErrorHandler(otel.ErrorHandlerFunc(onError))
	exporter, err := newExporter(endpoint, environ)
	if err != nil {
		return nil, err
	}
	return newSpans(exporter, service, version)
}

// newSpans returns Spans exporting with exporter.
func newSpans(exporter sdktrace.SpanExporter, service, version string) (*Spans, error) {
	res, err := resource.Merge(
		resource.NewSchemaless(attribute.String("service.name", service), attribute.String("service.version", version)),
		resource.Environment(), // OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES win
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithIDGenerator(ids{}),
	)
	return &Spans{provider: provider, tracer: provider.Tracer("github.com/ajlatour/golang-vibe-starter/internal/telemetry")}, nil
}

// Shutdown exports the spans still waiting, and stops: call it as the
// service stops, with a deadline.
func (s *Spans) Shutdown(ctx context.Context) error { return s.provider.Shutdown(ctx) }

// Start starts a span as tracectx.Spans describes, in the trace it's given.
func (s *Spans) Start(ctx context.Context, sp tracectx.Span) (tracectx.Parent, func(tracectx.End)) {
	kind := trace.SpanKindServer
	if sp.Kind == tracectx.Client {
		kind = trace.SpanKindClient
	}
	if sp.Parent.IsRoot() {
		// A trace this service started: the SDK makes the root span, in
		// the trace already logged (ids).
		ctx = context.WithValue(ctx, rootTraceKey{}, trace.TraceID(sp.Parent.TraceID))
	} else {
		ctx = trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    sp.Parent.TraceID,
			SpanID:     sp.Parent.SpanID,
			TraceFlags: trace.TraceFlags(sp.Parent.Flags),
			Remote:     true,
		}))
	}
	_, span := s.tracer.Start(ctx, sp.Name, trace.WithSpanKind(kind),
		trace.WithAttributes(attribute.String("http.request.method", sp.Method)))
	sc := span.SpanContext()
	started := tracectx.Parent{TraceID: sc.TraceID(), SpanID: sc.SpanID(), Flags: byte(sc.TraceFlags())}
	return started, func(e tracectx.End) { end(span, sp, e) }
}

// end ends span with how it went: the status, the route a server span
// matched (and is named for), and why a call failed. A 5xx is an error on
// either side; a 4xx only for a client span, the client's own mistake.
func end(span trace.Span, sp tracectx.Span, e tracectx.End) {
	if !span.IsRecording() {
		span.End()
		return
	}
	if e.Status != 0 {
		span.SetAttributes(attribute.Int("http.response.status_code", e.Status))
	}
	if e.Route != "" {
		span.SetName(e.Route)
		span.SetAttributes(attribute.String("http.route", e.Route))
	}
	switch {
	case e.Err != nil:
		span.SetStatus(codes.Error, e.Err.Error())
	case e.Status >= 500 || (sp.Kind == tracectx.Client && e.Status >= 400):
		span.SetStatus(codes.Error, strconv.Itoa(e.Status))
	}
	span.End()
}

// rootTraceKey carries a root span's trace ID to ids.
type rootTraceKey struct{}

// ids makes span and trace IDs: random, but for a root span, the trace ID
// the service already gave the request, and logged.
type ids struct{}

func (ids) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	id, ok := ctx.Value(rootTraceKey{}).(trace.TraceID)
	if !ok || !id.IsValid() {
		_, _ = rand.Read(id[:])
	}
	return id, ids{}.NewSpanID(ctx, id)
}

func (ids) NewSpanID(context.Context, trace.TraceID) trace.SpanID {
	var id trace.SpanID
	for !id.IsValid() {
		_, _ = rand.Read(id[:])
	}
	return id
}
