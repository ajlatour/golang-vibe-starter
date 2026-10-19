package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

func recorded(t *testing.T) (*Spans, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	s, err := newSpans(exp, "test-service", "v1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return s, exp
}

// flush exports what's been ended, and returns every span exported.
func flush(t *testing.T, s *Spans, exp *tracetest.InMemoryExporter) tracetest.SpanStubs {
	t.Helper()
	if err := s.provider.ForceFlush(t.Context()); err != nil {
		t.Fatal(err)
	}
	return exp.GetSpans()
}

func attr(stub tracetest.SpanStub, key string) attribute.Value {
	for _, kv := range stub.Attributes {
		if string(kv.Key) == key {
			return kv.Value
		}
	}
	return attribute.Value{}
}

// TestSpansFollowTheTrace checks a request's span is the caller's span's
// child, in its trace, named for its route once known; that a call's span is
// the request's child; and that each returned context is the span's own.
func TestSpansFollowTheTrace(t *testing.T) {
	s, exp := recorded(t)
	caller, _ := tracectx.Parse("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	server, endServer := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Server, Name: "GET", Method: "GET", Parent: caller})
	client, endClient := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Client, Name: "upstream GET /items/x", Method: "GET", Parent: server})
	endClient(tracectx.End{Status: http.StatusNotFound})
	endServer(tracectx.End{Status: http.StatusOK, Route: "GET /items/{id}"})

	if server.TraceID != caller.TraceID || server.SpanID == caller.SpanID || server.Flags != 1 {
		t.Errorf("the request's span context %s: want a sampled span of its own in %s", server, caller)
	}
	if client.TraceID != caller.TraceID || client.SpanID == server.SpanID {
		t.Errorf("the call's span context %s: want a span of its own in the trace", client)
	}

	got := flush(t, s, exp)
	if len(got) != 2 {
		t.Fatalf("exported %d spans, want 2", len(got))
	}
	byKind := map[trace.SpanKind]tracetest.SpanStub{}
	for _, st := range got {
		byKind[st.SpanKind] = st
	}
	srv, cli := byKind[trace.SpanKindServer], byKind[trace.SpanKindClient]
	if srv.Name != "GET /items/{id}" || srv.Parent.SpanID() != caller.SpanID || srv.SpanContext.SpanID() != server.SpanID {
		t.Errorf("server span %q, parent %s: want it named for its route, the caller's child", srv.Name, srv.Parent.SpanID())
	}
	if attr(srv, "http.route").AsString() != "GET /items/{id}" || attr(srv, "http.response.status_code").AsInt64() != 200 || srv.Status.Code == codes.Error {
		t.Errorf("server span attributes %v, status %v", srv.Attributes, srv.Status)
	}
	if cli.Name != "upstream GET /items/x" || cli.Parent.SpanID() != server.SpanID || cli.Status.Code != codes.Error {
		t.Errorf("client span %q, parent %s, status %v: want the request's child, a 404 an error", cli.Name, cli.Parent.SpanID(), cli.Status)
	}
	if v := attr(srv, "http.request.method").AsString(); v != "GET" {
		t.Errorf("method %q", v)
	}
	if res := srv.Resource.Attributes(); !hasAttr(res, "service.name", "test-service") || !hasAttr(res, "service.version", "v1") {
		t.Errorf("resource %v: want the service's name and version", res)
	}
}

func hasAttr(kvs []attribute.KeyValue, key, value string) bool {
	for _, kv := range kvs {
		if string(kv.Key) == key && kv.Value.AsString() == value {
			return true
		}
	}
	return false
}

// TestRootSpanKeepsTheTrace checks a trace the service started is the root
// span's trace: the ID already logged.
func TestRootSpanKeepsTheTrace(t *testing.T) {
	s, exp := recorded(t)
	root := tracectx.New()
	span, end := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Server, Name: "GET", Method: "GET", Parent: root})
	end(tracectx.End{Status: http.StatusInternalServerError})
	if span.TraceID != root.TraceID || span.IsRoot() {
		t.Errorf("span context %s, want a span in the trace %x", span, root.TraceID)
	}
	got := flush(t, s, exp)
	if len(got) != 1 || got[0].Parent.IsValid() || got[0].SpanContext.TraceID() != root.TraceID || got[0].Status.Code != codes.Error {
		t.Errorf("exported %+v: want one root span in the logged trace, a 500 an error", got)
	}
}

// TestUnsampled checks a span the sampler drops still has a context in the
// trace, unsampled, for the calls to carry on, and isn't exported.
func TestUnsampled(t *testing.T) {
	s, exp := recorded(t)
	caller, _ := tracectx.Parse("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
	span, end := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Server, Name: "GET", Parent: caller})
	end(tracectx.End{Status: 200, Route: "GET /x"})
	if span.TraceID != caller.TraceID || span.Flags != 0 {
		t.Errorf("span context %s, want the caller's trace, unsampled as the caller asked", span)
	}
	if got := flush(t, s, exp); len(got) != 0 {
		t.Errorf("exported %d spans of an unsampled trace", len(got))
	}
}

// TestClientError checks a call that failed is an error, saying why.
func TestClientError(t *testing.T) {
	s, exp := recorded(t)
	_, end := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Client, Name: "upstream GET /x", Parent: tracectx.New()})
	end(tracectx.End{Err: errors.New("connection refused")})
	got := flush(t, s, exp)
	if len(got) != 1 || got[0].Status.Code != codes.Error || got[0].Status.Description != "connection refused" {
		t.Errorf("exported %+v: want an error saying why", got)
	}
}

// TestNewExports checks New exports over OTLP/HTTP to the endpoint it's
// given, at shutdown at the latest, and tells onError of an
// export that fails.
func TestNewExports(t *testing.T) {
	var posts atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/traces" {
			posts.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	s, err := New(t.Context(), collector.URL+"/v1/traces", "test-service", "v1", func(err error) { t.Errorf("onError(%v)", err) })
	if err != nil {
		t.Fatal(err)
	}
	_, end := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Server, Name: "GET", Parent: tracectx.New()})
	end(tracectx.End{Status: 200})
	if err := s.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if posts.Load() == 0 {
		t.Error("nothing was exported to the collector")
	}

	t.Run("a collector that refuses", func(t *testing.T) {
		refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer refusing.Close()
		var failed atomic.Int32
		s, err := New(t.Context(), refusing.URL+"/v1/traces", "test-service", "v1", func(error) { failed.Add(1) })
		if err != nil {
			t.Fatal(err)
		}
		_, end := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Server, Name: "GET", Parent: tracectx.New()})
		end(tracectx.End{Status: 200})
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		if failed.Load() == 0 {
			t.Error("onError wasn't told of the failed export")
		}
	})
}

var _ sdktrace.SpanExporter = (*tracetest.InMemoryExporter)(nil)
