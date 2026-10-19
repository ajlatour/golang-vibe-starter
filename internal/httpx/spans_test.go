package httpx_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

// recordedSpans is a tracectx.Spans that keeps what it's told, giving each
// span a fixed ID and sampling it.
type recordedSpans struct {
	mu     sync.Mutex
	starts []tracectx.Span
	ends   []tracectx.End
}

var spanID = [8]byte{1, 2, 3, 4, 5, 6, 7, 8}

func (s *recordedSpans) Start(_ context.Context, sp tracectx.Span) (tracectx.Parent, func(tracectx.End)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, sp)
	return tracectx.Parent{TraceID: sp.Parent.TraceID, SpanID: spanID, Flags: 1}, func(e tracectx.End) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.ends = append(s.ends, e)
	}
}

// TestSpans checks WithSpans records each request's span: a child of the
// caller's, or a root of a trace of the service's own, ended with the status
// and route; and that the request carries the span's context, and the spans,
// to the calls it makes.
func TestSpans(t *testing.T) {
	logger := slog.New(logx.NewContextHandler(slog.NewJSONHandler(io.Discard, nil)))
	const caller = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	callerTrace, _ := tracectx.Parse(caller)

	for name, tt := range map[string]struct {
		traceparent string
		root        bool
	}{
		"a caller's trace":     {traceparent: caller},
		"a trace of its own":   {root: true},
		"a malformed caller's": {traceparent: "00-nope", root: true},
	} {
		t.Run(name, func(t *testing.T) {
			spans := &recordedSpans{}
			var inner tracectx.Parent
			var innerSpans tracectx.Spans
			mux := http.NewServeMux()
			mux.HandleFunc("GET /things/{id}", func(w http.ResponseWriter, r *http.Request) {
				inner, _ = logx.Trace(r.Context())
				innerSpans = logx.Spans(r.Context())
				w.WriteHeader(http.StatusTeapot)
			})
			h := httpx.Middleware(logger, httpx.RecordRoute(mux), httpx.WithSpans(spans))

			req := httptest.NewRequest("GET", "/things/a", nil)
			if tt.traceparent != "" {
				req.Header.Set("traceparent", tt.traceparent)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			if len(spans.starts) != 1 {
				t.Fatalf("started %d spans, want 1", len(spans.starts))
			}
			s := spans.starts[0]
			if s.Kind != tracectx.Server || s.Name != "GET" || s.Method != "GET" || s.Parent.IsRoot() != tt.root {
				t.Errorf("started %+v, want a server span, root %v", s, tt.root)
			}
			if !tt.root && s.Parent != callerTrace {
				t.Errorf("parent %s, want the caller's, %s", s.Parent, callerTrace)
			}
			if inner.SpanID != spanID || inner.TraceID != s.Parent.TraceID || innerSpans != spans {
				t.Errorf("the handler's trace is %s, spans %v: want the span's context, and the spans", inner, innerSpans)
			}
			if len(spans.ends) != 1 || spans.ends[0] != (tracectx.End{Status: http.StatusTeapot, Route: "GET /things/{id}"}) {
				t.Errorf("ended %+v, want 418 on GET /things/{id}", spans.ends)
			}
		})
	}
}

// TestSpansExempt checks a request for an exempt path records no span.
func TestSpansExempt(t *testing.T) {
	logger := slog.New(logx.NewContextHandler(slog.NewJSONHandler(io.Discard, nil)))
	spans := &recordedSpans{}
	h := httpx.Middleware(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), httpx.WithSpans(spans, "/healthz"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/other", nil))
	if len(spans.starts) != 1 || len(spans.ends) != 1 {
		t.Errorf("started %d spans and ended %d, want 1 each: none for /healthz", len(spans.starts), len(spans.ends))
	}
}

// TestNoSpans checks that without WithSpans nothing records a request's
// span, and the calls it makes have no spans to record theirs in.
func TestNoSpans(t *testing.T) {
	logger := slog.New(logx.NewContextHandler(slog.NewJSONHandler(io.Discard, nil)))
	var spans tracectx.Spans = &recordedSpans{}
	h := httpx.Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spans = logx.Spans(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if spans != nil {
		t.Errorf("the request has spans %v, want none", spans)
	}
}
