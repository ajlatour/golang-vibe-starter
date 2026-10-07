package httpx

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// Middleware wraps h with the standard stack: the defensive response headers
// and a correlation ID for every request, then request logging around panic
// recovery, so a recovered panic is still logged as a 500. Log through a
// handler from logx.NewContextHandler to get request_id on each line.
func Middleware(logger *slog.Logger, h http.Handler, opts ...MiddlewareOption) http.Handler {
	var o middlewareOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.timeout > 0 {
		h = withDeadline(o.timeout, h)
	}
	return defensiveHeaders(assignRequestID(logRequests(logger, o, recoverPanics(logger, h))))
}

// MiddlewareOption adds to what Middleware does.
type MiddlewareOption func(*middlewareOptions)

type middlewareOptions struct {
	metrics  Metrics
	clientIP bool
	timeout  time.Duration
}

// Metrics records each request: Start as it arrives, Observe as its response
// is sent, with the route pattern it matched ("" if none), its method, status
// and duration. httpmetrics.Recorder is one.
type Metrics interface {
	Start()
	Observe(route, method string, status int, d time.Duration)
}

// WithMetrics records every request in m.
func WithMetrics(m Metrics) MiddlewareOption {
	return func(o *middlewareOptions) { o.metrics = m }
}

// WithClientIP adds client_ip, the address the request's connection came
// from, to "request received" and "response sent". It's the connection's
// address: proxy headers such as X-Forwarded-For are never trusted (the
// header guard removes them), so behind a load balancer it's the balancer's.
// An IP address is personal data in many jurisdictions, which is why this is
// an option.
func WithClientIP() MiddlewareOption {
	return func(o *middlewareOptions) { o.clientIP = true }
}

// WithRequestTimeout gives every request a deadline, d after it arrives:
// its context is done then, so the calls it's waiting on (the upstream, the
// validation service, flagd) give up, and the handler answers in time,
// usually 504. Serve's write timeout must be longer than d (see
// WriteTimeout), or a slow request's answer is cut off unsent while its log
// line says it was sent. Inside the logging and panic recovery, so a request
// that hits it is logged like any other.
func WithRequestTimeout(d time.Duration) MiddlewareOption {
	return func(o *middlewareOptions) { o.timeout = d }
}

// withDeadline is WithRequestTimeout's middleware.
func withDeadline(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientIP is the IP address r's connection came from.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// defensiveHeaders sets headers every response gets, errors included:
// X-Content-Type-Options: nosniff, so a browser never guesses a type other
// than the declared one (and so can't be led to run a JSON body as script),
// and Cache-Control: no-store, so neither a browser nor a proxy keeps a copy
// of what may be a client's data.
func defensiveHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assigned, not Set, so every response shares these values instead of
		// allocating its own. Each slice's length is its capacity, so a later
		// Add appends to a copy, never to the shared array.
		h := w.Header()
		h["X-Content-Type-Options"] = nosniff
		h["Cache-Control"] = noStore
		// Which API version answered (APIVersion), on every response,
		// those refused before the version is checked included.
		h[canonicalAPIVersion] = currentAPIVersion
		next.ServeHTTP(w, r)
	})
}

var (
	nosniff = []string{"nosniff"}
	noStore = []string{"no-store"}
)

// StatusClientClosedRequest is the status logged (and sent, though no one is
// listening) when the client disconnects before the response is ready. It is
// nginx's non-standard 499, widely recognised in log tooling.
const StatusClientClosedRequest = 499

// responseLevel picks the level of the "response sent" line: server failures
// are errors, so alerting on level=ERROR finds them; the container's
// healthcheck and readiness probes are debug, because they fire every few
// seconds and say nothing unless they fail; everything else, including 4xx (the client's mistake, not
// an incident), is info.
func responseLevel(r *http.Request, status int) slog.Level {
	if status >= 500 {
		return slog.LevelError
	}
	return receivedLevel(r)
}

// receivedLevel picks the level of the "request received" line: info, except
// for the healthcheck and readiness probes, which are debug.
func receivedLevel(r *http.Request) slog.Level {
	if r.URL.Path == HealthzPath || r.URL.Path == ReadyzPath {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// logRequests logs the two ends of every request: "request received" when it
// arrives, and "response sent" when the handler is done, with the status,
// size and duration. The first is what shows a request that never finishes;
// metadata only, never headers or bodies.
func logRequests(logger *slog.Logger, o middlewareOptions, next http.Handler) http.Handler {
	metrics := o.metrics
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if metrics != nil {
			metrics.Start()
		}
		// No route yet: ServeMux matches it inside next, so only the raw
		// path identifies the request at this point.
		received := [4]slog.Attr{
			slog.String(logx.KeyMethod, r.Method),
			slog.String(logx.KeyPath, logx.Truncate(r.URL.Path, logx.MaxLoggedPath)),
			slog.Int64(logx.KeyContentLength, r.ContentLength),
		}
		n := 3
		if o.clientIP {
			received[3], n = slog.String(logx.KeyClientIP, clientIP(r)), 4
		}
		logger.LogAttrs(r.Context(), receivedLevel(r), "request received", received[:n]...)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		if metrics != nil {
			// Deferred, so a request that ends in a panic recoverPanics passes
			// on (http.ErrAbortHandler) is still counted as finished, and the
			// in-flight gauge doesn't drift.
			defer func() { metrics.Observe(route(r), r.Method, rec.status, time.Since(start)) }()
		}
		next.ServeHTTP(rec, r)
		// The route is known once the handler has returned: RecordRoute
		// noted it (see route).
		// Room for every field "response sent" can have, so appending them
		// never has to grow the slice.
		attrs := appendRequestAttrs(make([]slog.Attr, 0, 9), r)
		attrs = append(attrs,
			slog.Int(logx.KeyStatus, rec.status),
			slog.Int(logx.KeyBytes, rec.bytes),
			logx.DurationMS(time.Since(start)),
		)
		if o.clientIP {
			attrs = append(attrs, slog.String(logx.KeyClientIP, clientIP(r)))
		}
		if rec.errMsg != "" {
			// What the client was told, which may quote its own input (a
			// JSON field name, say): bounded like any outside value.
			attrs = append(attrs,
				slog.String(logx.KeyResponseCode, string(rec.errCode)),
				slog.String(logx.KeyResponseError, logx.Truncate(rec.errMsg, maxLoggedResponseError)))
		}
		logger.LogAttrs(r.Context(), responseLevel(r, rec.status), "response sent", attrs...)
	})
}

// requestAttrs identifies a request in a log line: method, route pattern (for
// grouping, e.g. "GET /items/{id}"; empty if no route matched) and raw path.
func requestAttrs(r *http.Request) []slog.Attr {
	return appendRequestAttrs(make([]slog.Attr, 0, 3), r)
}

// appendRequestAttrs appends requestAttrs(r) to attrs.
func appendRequestAttrs(attrs []slog.Attr, r *http.Request) []slog.Attr {
	return append(attrs,
		slog.String(logx.KeyMethod, r.Method),
		slog.String(logx.KeyRoute, route(r)),
		slog.String(logx.KeyPath, logx.Truncate(r.URL.Path, logx.MaxLoggedPath)),
	)
}

// recoverPanics turns a handler panic into a 500 instead of a dropped
// connection, and logs the stack.
func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v) // deliberate abort; let net/http handle it
			}
			logger.LogAttrs(r.Context(), slog.LevelError, "panic serving request",
				append(requestAttrs(r),
					slog.Any(logx.KeyPanic, v),
					slog.String(logx.KeyStack, string(debug.Stack())),
				)...,
			)
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
		}()
		next.ServeHTTP(w, r)
	})
}

// maxLoggedResponseError caps the response_error logged.
const maxLoggedResponseError = 200

// statusRecorder captures the status code and body size written by a
// handler, and the error message WriteError sent, if any.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
	errMsg      string
	errCode     Code
}

// noteError tells the statusRecorder under w, if there is one, the error
// message the response sends. Handlers in between may wrap w, so it looks
// through each Unwrap.
func noteError(w http.ResponseWriter, code Code, msg string) {
	for {
		switch rw := w.(type) {
		case *statusRecorder:
			rw.errMsg, rw.errCode = msg, code
			return
		case interface{ Unwrap() http.ResponseWriter }:
			w = rw.Unwrap()
		default:
			return
		}
	}
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer
// (for Flush, deadlines, etc.).
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// RecordRoute wraps a router (an http.ServeMux) so the route pattern it
// matches is known to the middleware outside it, for the "response sent" line
// and the metrics. ServeMux sets the pattern on the request it was given,
// which is a copy whenever a handler in between called WithContext (as
// authentication does), so the outer request never sees it otherwise. Wrap
// the router itself, innermost.
func RecordRoute(router http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { logx.SetRoute(r.Context(), r.Pattern) }() // even if the handler panics
		router.ServeHTTP(w, r)
	})
}

// route is the pattern r matched: set on r itself inside the router, or
// recorded by RecordRoute for the copies outside it. "" if none matched.
func route(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return logx.Route(r.Context())
}
