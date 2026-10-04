package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Middleware wraps h with the standard stack: a correlation ID for every
// request, then request logging around panic recovery, so a recovered panic
// is still logged as a 500. Log through a handler from NewContextHandler to
// get request_id on each line.
func Middleware(logger *slog.Logger, h http.Handler) http.Handler {
	return assignRequestID(logRequests(logger, recoverPanics(logger, h)))
}

// StatusClientClosedRequest is the status logged (and sent, though no one is
// listening) when the client disconnects before the response is ready. It is
// nginx's non-standard 499, widely recognised in log tooling.
const StatusClientClosedRequest = 499

// requestLevel picks the level of a request's log line: server failures are
// errors, so alerting on level=ERROR finds them; the container's healthcheck
// is debug, because it fires every few seconds and says nothing unless it
// fails; everything else, including 4xx (the client's mistake, not an
// incident), is info.
func requestLevel(r *http.Request, status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case r.URL.Path == HealthzPath:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// logRequests logs one line per request with its status and duration, at the
// level requestLevel picks.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// ServeMux sets r.Pattern on this same request when it matches a
		// route, so it's available once the handler has returned.
		logger.LogAttrs(r.Context(), requestLevel(r, rec.status), "request",
			append(requestAttrs(r),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				DurationMS(time.Since(start)),
			)...,
		)
	})
}

// requestAttrs identifies a request in a log line: method, route pattern (for
// grouping, e.g. "GET /items/{id}"; empty if no route matched) and raw path.
func requestAttrs(r *http.Request) []slog.Attr {
	return []slog.Attr{
		slog.String(LogKeyMethod, r.Method),
		slog.String(LogKeyRoute, r.Pattern),
		slog.String(LogKeyPath, r.URL.Path),
	}
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
					slog.Any("panic", v),
					slog.String("stack", string(debug.Stack())),
				)...,
			)
			WriteError(w, http.StatusInternalServerError, "internal server error")
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code and body size written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
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
