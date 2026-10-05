package httpx_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// BenchmarkMiddleware measures the standard stack around every request:
// correlation ID, the request and response log lines (JSON, discarded), and
// panic recovery, around a handler that writes a small JSON body.
func BenchmarkMiddleware(b *testing.B) {
	logger := logx.New(io.Discard, slog.LevelInfo, "json", "api", "bench")
	h := httpx.Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	for name, id := range map[string]string{"generated ID": "", "caller's ID": "4LSBQEPNJ5NMWEBHAE7IJOPYIX"} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				req := httptest.NewRequest(http.MethodGet, "/items", nil)
				if id != "" {
					req.Header.Set(httpx.RequestIDHeader, id)
				}
				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
