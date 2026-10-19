// Package logx is the service's structured logging toolkit: the logger
// itself, the field names every log line uses, request-ID correlation, and
// the helpers that keep logged values bounded and free of credentials. It
// imports nothing from this module, so every package can use it.
package logx

import (
	"context"
	"io"
	"log/slog"
	"runtime/debug"
	"sync"

	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

// New returns a logger writing to w at level in the given format ("text" for
// key=value lines, anything else for JSON). Every line carries service and
// version, and lines logged with a request's context carry its request_id.
func New(w io.Writer, level slog.Level, format, service, version string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(w, opts)
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(NewContextHandler(h)).With(KeyService, service, KeyVersion, version)
}

type requestIDKey struct{}

// requestState is what the log lines of one request share: its correlation
// ID, and what is learned about the request as it's served, by handlers
// further in. Middleware hands handlers copies of the request (each
// WithContext makes one), so what the inner ones learn would otherwise be lost
// to the outer ones; the state is shared by pointer, so every copy sees it.
// One allocation per request, as storing the ID alone would cost.
type requestState struct {
	id string

	// The request's W3C trace context (SetTrace), and its trace ID as
	// logged, made once rather than on every line.
	trace    tracectx.Parent
	traceID  string
	hasTrace bool
	// What records the request's spans (SetSpans), if anything does.
	spans tracectx.Spans

	mu    sync.Mutex
	route string
	attrs []slog.Attr
	buf   [2]slog.Attr // room for the first attrs without allocating
}

func state(ctx context.Context) *requestState {
	s, _ := ctx.Value(requestIDKey{}).(*requestState)
	return s
}

// RequestID returns the request's correlation ID, or "" if ctx has none.
func RequestID(ctx context.Context) string {
	if s := state(ctx); s != nil {
		return s.id
	}
	return ""
}

// SetRoute records the route pattern the request matched, for the log lines
// written outside the router, where the request no longer carries it. A
// context without WithRequestID ignores it.
func SetRoute(ctx context.Context, route string) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		s.route = route
		s.mu.Unlock()
	}
}

// Route returns the route recorded with SetRoute, or "".
func Route(ctx context.Context) string {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.route
	}
	return ""
}

// AddAttrs adds fields to every later log line of the request, those written
// by outer handlers included, such as which API key made it. A context
// without WithRequestID ignores them.
func AddAttrs(ctx context.Context, attrs ...slog.Attr) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		s.attrs = append(s.attrs, attrs...)
		s.mu.Unlock()
	}
}

// SetTrace records the request's trace context: every later log line of the
// request carries its trace ID, and outbound calls (internal/jsoncall) carry
// it on. Call it as the request arrives, and again with its own span's
// context if one is recorded (SetSpans); a context without WithRequestID
// ignores it.
func SetTrace(ctx context.Context, p tracectx.Parent) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		s.trace, s.traceID, s.hasTrace = p, p.TraceIDString(), true
		s.mu.Unlock()
	}
}

// Trace returns the request's trace context, if SetTrace recorded one.
func Trace(ctx context.Context) (tracectx.Parent, bool) {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.trace, s.hasTrace
	}
	return tracectx.Parent{}, false
}

// SetSpans records what records the request's spans, so the calls it makes
// (internal/jsoncall) record theirs. A context without WithRequestID ignores
// it.
func SetSpans(ctx context.Context, s tracectx.Spans) {
	if st := state(ctx); st != nil {
		st.mu.Lock()
		st.spans = s
		st.mu.Unlock()
	}
}

// Spans returns what SetSpans recorded, or nil.
func Spans(ctx context.Context) tracectx.Spans {
	if s := state(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.spans
	}
	return nil
}

// WithRequestID returns a copy of ctx carrying id as the correlation ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	s := &requestState{id: id}
	s.attrs = s.buf[:0]
	return context.WithValue(ctx, requestIDKey{}, s)
}

// NewContextHandler wraps h so that every record logged with a context
// carrying a correlation ID (see RequestID) gets a request_id attribute.
// Code only needs to use the *Context logging methods to be correlated.
func NewContextHandler(h slog.Handler) slog.Handler {
	return contextHandler{h}
}

type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if s := state(ctx); s != nil {
		r.AddAttrs(slog.String(KeyRequestID, s.id))
		s.mu.Lock()
		if s.hasTrace {
			r.AddAttrs(slog.String(KeyTraceID, s.traceID))
		}
		r.AddAttrs(s.attrs...)
		s.mu.Unlock()
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// BuildVersion describes the running binary from its build info: the VCS
// commit (with "-dirty" if the tree had uncommitted changes) when Go stamped
// one, else the module version, else "devel".
func BuildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if dirty {
			rev += "-dirty"
		}
		return rev
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return "devel"
}
