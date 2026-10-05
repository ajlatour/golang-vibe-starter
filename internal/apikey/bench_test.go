package apikey_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
)

// BenchmarkMiddleware measures checking a request's key: one SHA-256 and a
// constant-time comparison against every configured key.
func BenchmarkMiddleware(b *testing.B) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	for _, n := range []int{1, 10} {
		var entries []string
		for i := range n {
			entries = append(entries, fmt.Sprintf("k%d:write:%s", i, apikey.HashHex(fmt.Sprintf("gvs_key-%d", i))))
		}
		keys, err := apikey.ParseKeys(strings.Join(entries, ","))
		if err != nil {
			b.Fatal(err)
		}
		h := apikey.Middleware(slog.New(slog.DiscardHandler), apikey.Policy{Keys: keys}, next)
		b.Run(fmt.Sprintf("%d keys", n), func(b *testing.B) {
			req := httptest.NewRequest("GET", "/items", nil)
			req.Header.Set("Authorization", "Bearer gvs_key-0")
			w := httptest.NewRecorder()
			for b.Loop() {
				h.ServeHTTP(w, req)
			}
		})
	}
}
