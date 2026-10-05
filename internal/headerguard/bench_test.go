package headerguard_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
)

// BenchmarkMiddleware measures the guard on every request: a typical client's
// headers that pass, and a request with a proxy header to strip.
func BenchmarkMiddleware(b *testing.B) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	guard := headerguard.Middleware(slog.New(slog.DiscardHandler), headerguard.DefaultPolicy(), next)
	typical := func(r *http.Request) *http.Request {
		for k, v := range map[string]string{
			"Accept": "application/json", "Accept-Encoding": "gzip", "User-Agent": "client/1.0",
			"Authorization": "Bearer " + strings.Repeat("t", 200), "Client-Correlation-Id": "4LSBQEPNJ5NMWEBHAE7IJOPYIX",
			"Connection": "keep-alive", "Cache-Control": "no-cache",
		} {
			r.Header.Set(k, v)
		}
		return r
	}
	b.Run("GET, passes", func(b *testing.B) {
		req := typical(httptest.NewRequest(http.MethodGet, "/items", nil))
		w := httptest.NewRecorder()
		for b.Loop() {
			guard.ServeHTTP(w, req)
		}
	})
	b.Run("POST JSON, passes", func(b *testing.B) {
		req := typical(httptest.NewRequest(http.MethodPost, "/items", http.NoBody))
		req.ContentLength = 20
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		w := httptest.NewRecorder()
		for b.Loop() {
			guard.ServeHTTP(w, req)
		}
	})
	// Stripping changes the request, so this one builds a fresh request each
	// time, and that cost is in its numbers.
	b.Run("GET, strips a proxy header", func(b *testing.B) {
		w := httptest.NewRecorder()
		for b.Loop() {
			req := typical(httptest.NewRequest(http.MethodGet, "/items", nil))
			req.Header.Set("X-Forwarded-For", "203.0.113.7")
			guard.ServeHTTP(w, req)
		}
	})
}
