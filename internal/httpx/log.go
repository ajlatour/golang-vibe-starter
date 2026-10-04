package httpx

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"
)

// Log field names shared across the service. Every log line uses these
// names for these values, so one query finds them all; the README lists the
// full schema.
const (
	LogKeyRequestID  = "request_id"
	LogKeyError      = "error"
	LogKeyMethod     = "method"
	LogKeyRoute      = "route"
	LogKeyPath       = "path"
	LogKeyDurationMS = "duration_ms"
)

// NewContextHandler wraps h so that every record logged with a context
// carrying a correlation ID (see RequestID) gets a request_id attribute.
// Code only needs to use the *Context logging methods to be correlated.
func NewContextHandler(h slog.Handler) slog.Handler {
	return contextHandler{h}
}

type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String(LogKeyRequestID, id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// DurationMS returns d as a duration_ms attribute: milliseconds as a decimal
// number, which reads and aggregates better than slog's nanosecond integers.
func DurationMS(d time.Duration) slog.Attr {
	return slog.Float64(LogKeyDurationMS, float64(d.Microseconds())/1000)
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
