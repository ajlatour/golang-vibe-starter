package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"runtime/debug"
	"time"
	"unicode/utf8"
)

// Log field names. This is the one list: every log line uses these names for
// these values, so a single query finds them all, and a test fails the build
// if code passes a raw string as a key instead. The README documents each.
const (
	// On every line.
	LogKeyService   = "service"
	LogKeyVersion   = "version"
	LogKeyRequestID = "request_id" // within a request, added from its context

	// The request and response (see logRequests).
	LogKeyMethod = "method"
	LogKeyRoute  = "route" // matched pattern, e.g. "GET /items/{id}"
	LogKeyPath   = "path"  // URL path only, never the query string
	// LogKeyContentLength is the request body size the client declared, or -1
	// if unknown (e.g. chunked). Only the number is logged, never the body.
	LogKeyContentLength = "content_length"
	LogKeyStatus        = "status"
	LogKeyBytes         = "bytes"
	LogKeyDurationMS    = "duration_ms"

	// Failures.
	LogKeyError = "error"
	LogKeyPanic = "panic"
	LogKeyStack = "stack"

	// Upstream calls. Prefixed "upstream_" so they can't be confused with the
	// inbound request's method, path and status.
	LogKeyUpstream           = "upstream" // "METHOD /path" of the call
	LogKeyUpstreamStatus     = "upstream_status"
	LogKeyUpstreamDurationMS = "upstream_duration_ms"

	// Feature flags.
	LogKeyFlag      = "flag"
	LogKeyDefault   = "default"
	LogKeyFallbacks = "fallbacks"

	// Startup and shutdown.
	LogKeyURL     = "url"
	LogKeyTimeout = "timeout"
	LogKeyAddr    = "addr"
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
	return DurationAttr(LogKeyDurationMS, d)
}

// DurationAttr is DurationMS under a different key, such as
// "upstream_duration_ms". Keys should end in _ms.
func DurationAttr(key string, d time.Duration) slog.Attr {
	return slog.Float64(key, float64(d.Microseconds())/1000)
}

// MaxLoggedPath caps the request and upstream paths written to logs, which
// are chosen by the caller and could otherwise be up to the server's header
// limit (1 MiB) per line.
const MaxLoggedPath = 512

// Truncate shortens s to at most n runes for logging, ending it with "…" when
// it cuts. Use it for any logged value that comes from outside the service.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// RedactURLError masks credentials in the URL that net/http puts in its
// transport errors (*url.Error). The client already hides a password but
// leaves the username, and the rule for logs is that URLs go through
// (*url.URL).Redacted. Wrap any error from http.Client.Do before it can reach
// a log line.
func RedactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	u, perr := url.Parse(ue.URL)
	if perr != nil {
		return err
	}
	return &url.Error{Op: ue.Op, URL: u.Redacted(), Err: ue.Err}
}

// LogAttrsError is implemented by errors that know extra fields worth
// logging, such as the status of a failed upstream call. ServerError adds
// them to its "internal error" line.
type LogAttrsError interface {
	error
	LogAttrs() []slog.Attr
}

// errorAttrs returns the error attribute for err, followed by the fields of
// the first LogAttrsError in its chain, if any.
func errorAttrs(err error) []slog.Attr {
	attrs := []slog.Attr{slog.Any(LogKeyError, err)}
	var la LogAttrsError
	if errors.As(err, &la) {
		attrs = append(attrs, la.LogAttrs()...)
	}
	return attrs
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
