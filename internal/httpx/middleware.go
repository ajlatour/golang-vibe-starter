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

// responseLevel picks the level of the "response sent" line: server failures
// are errors, so alerting on level=ERROR finds them; the container's
// healthcheck is debug, because it fires every few seconds and says nothing
// unless it fails; everything else, including 4xx (the client's mistake, not
// an incident), is info.
func responseLevel(r *http.Request, status int) slog.Level {
	if status >= 500 {
		return slog.LevelError
	}
	return receivedLevel(r)
}

// receivedLevel picks the level of the "request received" line: info, except
// for the healthcheck, which is debug.
func receivedLevel(r *http.Request) slog.Level {
	if r.URL.Path == HealthzPath {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// logRequests logs the two ends of every request: "request received" when it
// arrives, and "response sent" when the handler is done, with the status,
// size and duration. The first is what shows a request that never finishes;
// metadata only, never headers or bodies.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// No route yet: ServeMux matches it inside next, so only the raw
		// path identifies the request at this point.
		logger.LogAttrs(r.Context(), receivedLevel(r), "request received",
			slog.String(LogKeyMethod, r.Method),
			slog.String(LogKeyPath, Truncate(r.URL.Path, MaxLoggedPath)),
			slog.Int64(LogKeyContentLength, r.ContentLength),
		)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// ServeMux sets r.Pattern on this same request when it matches a
		// route, so it's available once the handler has returned.
		logger.LogAttrs(r.Context(), responseLevel(r, rec.status), "response sent",
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
		slog.String(LogKeyPath, Truncate(r.URL.Path, MaxLoggedPath)),
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
