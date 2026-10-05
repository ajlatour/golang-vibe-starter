package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDefensiveHeaders checks every kind of response carries the defensive
// headers: successes, the router's own 404, the header guard's refusals,
// validation failures and server errors.
func TestDefensiveHeaders(t *testing.T) {
	for _, tt := range []struct {
		name, env, method, path, contentType, body string
		want                                       int
	}{
		{"a success", "ok", "GET", "/items", "", "", 200},
		{"the healthcheck", "ok", "GET", "/healthz", "", "", 200},
		{"an unknown route", "ok", "GET", "/nope", "", "", 404},
		{"an invalid item ID", "ok", "GET", "/items/a%2Fb", "", "", 404},
		{"the header guard's refusal", "ok", "POST", "/items", "text/plain", `{"name":"x"}`, 415},
		{"a validation failure", "ok", "POST", "/items", "application/json", `{"name":""}`, 422},
		{"a server error", "internal", "GET", "/items", "", "", 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler, _ := handlerFor(t, tt.env)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+testWriteKey)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status %d, want %d", rec.Code, tt.want)
			}
			checkDefensiveHeaders(t, rec)
		})
	}
	t.Run("an authentication refusal", func(t *testing.T) {
		handler, _ := handlerFor(t, "ok")
		req := httptest.NewRequest("DELETE", "/items/x", nil)
		req.Header.Set("Authorization", "Bearer "+testReadKey)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("status %d, want 403", rec.Code)
		}
		checkDefensiveHeaders(t, rec)
	})
}

func checkDefensiveHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for name, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store"} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestServerCapsHeaders checks the running server refuses headers far over
// the header guard's limit itself, before reading 1 MiB of them, while the
// guard still answers a request just over its limit with JSON.
func TestServerCapsHeaders(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	addr, _, errc := startApp(t, ctx, map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "API_AUTH": "off"})
	defer func() { cancel(); waitForShutdown(t, errc) }()

	send := func(total int) (*http.Response, string) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/healthz", nil)
		for i := 0; total > 0; i++ {
			n := min(total, 8000)
			req.Header.Set(fmt.Sprintf("X-Pad-%d", i), strings.Repeat("a", n))
			total -= n
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body)
	}

	resp, body := send(40 << 10) // over the guard's 32 KiB, under the server's cap
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge || !strings.HasPrefix(body, `{"error"`) {
		t.Errorf("40 KiB of headers: %d %q; want the guard's JSON 431", resp.StatusCode, body)
	}
	resp, body = send(200 << 10) // far over both
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge || strings.HasPrefix(body, `{"error"`) {
		t.Errorf("200 KiB of headers: %d %q; want Go's own 431, before the guard", resp.StatusCode, body)
	}
}
