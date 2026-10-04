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

// RequestID returns the request's correlation ID, or "" if ctx has none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// WithRequestID returns a copy of ctx carrying id as the correlation ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// NewContextHandler wraps h so that every record logged with a context
// carrying a correlation ID (see RequestID) gets a request_id attribute.
// Code only needs to use the *Context logging methods to be correlated.
func NewContextHandler(h slog.Handler) slog.Handler {
	return contextHandler{h}
}

type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String(KeyRequestID, id))
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
