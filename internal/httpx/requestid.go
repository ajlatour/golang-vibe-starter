package httpx

import (
	"crypto/rand"
	"net/http"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

// RequestIDHeader carries the request's correlation ID. A caller may send one
// to tie the API's log lines to its own; otherwise the API generates one.
// Either way it is echoed in the response, so a client can quote it when
// reporting a problem.
const RequestIDHeader = "Client-Correlation-Id"

// maxRequestIDLen bounds an incoming correlation ID, so a caller can't put
// arbitrarily large values into every log line.
const maxRequestIDLen = 128

// assignRequestID gives each request a correlation ID: the caller's, if it
// sent a valid one, or a new random one. The ID goes into the request context
// (where the log handler picks it up) and the response header.
//
// It also takes the request's W3C trace context: the caller's traceparent, if
// it sent a valid one, or a new trace. Its trace ID goes on every log line of
// the request, and outbound calls carry the trace on (internal/tracectx).
func assignRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID(id) {
			id = rand.Text()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := logx.WithRequestID(r.Context(), id)
		trace, ok := tracectx.Parse(r.Header.Get(tracectx.Header))
		if !ok {
			trace = tracectx.New()
		}
		logx.SetTrace(ctx, trace)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// validRequestID accepts IDs of 1 to maxRequestIDLen characters drawn from
// letters, digits and "-_.:", which covers UUIDs, ULIDs and most tracing
// formats while keeping control characters and quoting out of the logs.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for _, c := range []byte(id) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}
