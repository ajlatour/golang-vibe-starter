package headerguard_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
)

// FuzzMiddleware throws arbitrary headers at the guard. Whatever they are, it
// lets the request through or refuses it with 400, 415 or 431 and a JSON
// error, and a header it strips never reaches the handler.
func FuzzMiddleware(f *testing.F) {
	for _, seed := range []struct{ method, headers, body string }{
		{"GET", "Accept: application/json", ""},
		{"POST", "Content-Type: application/json", "{}"},
		{"POST", "Content-Type: text/plain", "x"},
		{"POST", "", "x"},
		{"GET", "Client-Correlation-Id: a\nClient-Correlation-Id: b", ""},
		{"GET", "X-Forwarded-For: 1.2.3.4\nForwarded: for=x\nX-Http-Method-Override: DELETE", ""},
		{"GET", "X-Big: " + strings.Repeat("a", 9000), ""},
		{"PUT", "content-type: Application/JSON; charset=utf-8;;", "{}"},
	} {
		f.Add(seed.method, seed.headers, seed.body)
	}
	p := headerguard.DefaultPolicy()
	f.Fuzz(func(t *testing.T, method, headers, body string) {
		if method == "" || strings.ContainsAny(method, " \t\r\n") {
			method = "GET"
		}
		req := httptest.NewRequest("GET", "/items", strings.NewReader(body))
		req.Method = method
		for line := range strings.SplitSeq(headers, "\n") {
			name, value, _ := strings.Cut(line, ":")
			if name = strings.TrimSpace(name); name != "" {
				req.Header[http.CanonicalHeaderKey(name)] = append(req.Header[http.CanonicalHeaderKey(name)], strings.TrimSpace(value))
			}
		}
		var seen http.Header
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Clone()
			w.WriteHeader(http.StatusNoContent)
		})
		rec := httptest.NewRecorder()
		headerguard.Middleware(slog.New(slog.DiscardHandler), p, next).ServeHTTP(rec, req)

		switch rec.Code {
		case http.StatusNoContent:
			for _, h := range p.Strip {
				if _, ok := seen[http.CanonicalHeaderKey(h)]; ok {
					t.Errorf("the handler saw %s, which the guard strips", h)
				}
			}
		case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusRequestHeaderFieldsTooLarge:
			if seen != nil {
				t.Error("the handler ran for a refused request")
			}
			var e struct{ Error string }
			if json.Unmarshal(rec.Body.Bytes(), &e) != nil || e.Error == "" {
				t.Errorf("refused with %d and body %q, want a JSON error", rec.Code, rec.Body)
			}
		default:
			t.Errorf("status %d: the guard lets through (204) or refuses with 400, 415 or 431", rec.Code)
		}
	})
}
