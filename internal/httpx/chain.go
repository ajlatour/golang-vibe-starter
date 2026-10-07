package httpx

import "net/http"

// Chain wraps h in middleware, listed outermost first: Chain(h, a, b) is
// a(b(h)), so a request meets them in the order they're listed. Where the
// order matters, as it does for the service's protections, a list reads it
// where nested calls hide it.
func Chain(h http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
