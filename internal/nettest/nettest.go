// Package nettest provides test helpers for the ways an HTTP dependency can
// misbehave: refusing connections, never answering, dropping the connection,
// or answering with an error. It is imported only by tests, and imports
// nothing from this module.
package nettest

import (
	"net/http"
	"net/http/httptest"
)

// DeadURL returns the base URL of a server that was listening and has been
// shut down, so connecting to it is refused.
func DeadURL() string {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

// Hang never answers: it blocks until the client gives up. Use it to provoke
// client timeouts and cancellation. httptest.Server.Close waits for handlers
// to return, so the client must hang up, which its timeout does.
func Hang(_ http.ResponseWriter, r *http.Request) {
	<-r.Context().Done()
}

// Drop closes the connection without sending a response, as a crashed or
// reset upstream would.
func Drop(w http.ResponseWriter, _ *http.Request) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	conn.Close()
}

// Status returns a handler that replies with the given status and body. An
// empty body sends none.
func Status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}
}
