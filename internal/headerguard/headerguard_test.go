package headerguard_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

// serve sends req through the guard to a handler that records what it saw,
// and returns the response, whether the handler ran, the headers it saw, and
// the log lines.
func serve(t *testing.T, p headerguard.Policy, req *http.Request) (*httptest.ResponseRecorder, bool, http.Header, []map[string]any) {
	t.Helper()
	var logs logtest.Buffer
	reached := false
	var seen http.Header
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached, seen = true, r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	headerguard.Middleware(logs.Logger(slog.LevelDebug), p, next).ServeHTTP(rec, req)
	return rec, reached, seen, logs.Records(t)
}

func get(headers map[string][]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return req
}

func post(contentType, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

// manyHeaders returns n distinct headers.
func manyHeaders(n int) map[string][]string {
	h := map[string][]string{}
	for i := range n {
		h[fmt.Sprintf("X-H%d", i)] = []string{"v"}
	}
	return h
}

func TestRules(t *testing.T) {
	p := headerguard.DefaultPolicy()
	// A header whose name and value together are exactly n bytes.
	sized := func(n int) map[string][]string {
		name := "X-Big"
		return map[string][]string{name: {strings.Repeat("a", n-len(name))}}
	}
	tests := []struct {
		name       string
		req        *http.Request
		wantStatus int    // 0: let through
		wantReason string // the logged reason
		wantHeader string
	}{
		{name: "plain GET", req: get(nil)},
		{name: "100 headers", req: get(manyHeaders(100))},
		{name: "101 headers", req: get(manyHeaders(101)), wantStatus: 431, wantReason: "too many headers"},
		{name: "a header of exactly 8 KiB", req: get(sized(8 << 10))},
		{name: "a header over 8 KiB", req: get(sized(8<<10 + 1)), wantStatus: 431, wantReason: "header too large", wantHeader: "X-Big"},
		{
			name: "32 KiB in total", req: get(map[string][]string{
				"X-A": {strings.Repeat("a", 8<<10-3)}, "X-B": {strings.Repeat("a", 8<<10-3)},
				"X-C": {strings.Repeat("a", 8<<10-3)}, "X-D": {strings.Repeat("a", 8<<10-3)},
			}),
		},
		{
			name: "over 32 KiB in total", req: get(map[string][]string{
				"X-A": {strings.Repeat("a", 8<<10-3)}, "X-B": {strings.Repeat("a", 8<<10-3)},
				"X-C": {strings.Repeat("a", 8<<10-3)}, "X-D": {strings.Repeat("a", 8<<10-3)}, "X-E": {"a"},
			}),
			wantStatus: 431, wantReason: "headers too large",
		},
		{name: "one correlation ID", req: get(map[string][]string{"Client-Correlation-Id": {"a"}})},
		{name: "two correlation IDs", req: get(map[string][]string{"Client-Correlation-Id": {"a", "b"}}), wantStatus: 400, wantReason: "duplicate header", wantHeader: "Client-Correlation-Id"},
		{name: "two Authorization headers", req: get(map[string][]string{"Authorization": {"a", "b"}}), wantStatus: 400, wantReason: "duplicate header", wantHeader: "Authorization"},
		{name: "a repeatable header twice", req: get(map[string][]string{"Accept": {"a", "b"}})},
		{name: "JSON body", req: post("application/json", `{}`)},
		{name: "JSON body with charset", req: post("application/json; charset=utf-8", `{}`)},
		{name: "JSON body, upper case type", req: post("Application/JSON", `{}`)},
		{name: "body without Content-Type", req: post("", `{}`), wantStatus: 415, wantReason: "missing content type", wantHeader: "Content-Type"},
		{name: "text/plain body", req: post("text/plain", `{}`), wantStatus: 415, wantReason: "unsupported content type", wantHeader: "Content-Type"},
		{name: "XML body", req: post("application/xml", `<a/>`), wantStatus: 415, wantReason: "unsupported content type", wantHeader: "Content-Type"},
		{name: "unparseable Content-Type", req: post("application/json;;;", `{}`), wantStatus: 415, wantReason: "unsupported content type", wantHeader: "Content-Type"},
		{name: "POST without a body needs no Content-Type", req: post("", ``)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, reached, _, logs := serve(t, p, tt.req)
			if tt.wantStatus == 0 {
				if !reached || rec.Code != http.StatusNoContent {
					t.Fatalf("status = %d, reached = %v; want the request let through (logs: %v)", rec.Code, reached, logs)
				}
				return
			}
			if reached {
				t.Error("the handler ran; want the request stopped")
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			var body struct{ Error string }
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Errorf("body = %q, want a JSON error", rec.Body)
			}
			if len(logs) != 1 || logs[0]["msg"] != "request headers rejected" || logs[0]["level"] != "INFO" ||
				logs[0]["reason"] != tt.wantReason || logs[0]["status"] != float64(tt.wantStatus) {
				t.Errorf("logs = %v, want one INFO request headers rejected, reason %q, status %d", logs, tt.wantReason, tt.wantStatus)
			} else if got, _ := logs[0]["header"].(string); got != tt.wantHeader {
				t.Errorf("logged header = %q, want %q", got, tt.wantHeader)
			}
		})
	}
}

func TestReportMode(t *testing.T) {
	p := headerguard.DefaultPolicy()
	p.Mode = headerguard.Report
	rec, reached, _, logs := serve(t, p, post("text/plain", `{}`))
	if !reached || rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, reached = %v; report mode must let the request through", rec.Code, reached)
	}
	if len(logs) != 1 || logs[0]["msg"] != "request headers would be rejected" || logs[0]["reason"] != "unsupported content type" || logs[0]["status"] != float64(415) {
		t.Errorf("logs = %v, want one request headers would be rejected, reason unsupported content type, status 415", logs)
	}
}

func TestStrip(t *testing.T) {
	for _, mode := range []headerguard.Mode{headerguard.Enforce, headerguard.Report} {
		t.Run(mode.String(), func(t *testing.T) {
			p := headerguard.DefaultPolicy()
			p.Mode = mode
			req := get(map[string][]string{
				"X-Forwarded-For": {"1.2.3.4"}, "x-real-ip": {"1.2.3.4"}, "X-HTTP-Method-Override": {"DELETE"},
				"Forwarded": {"for=1.2.3.4"}, "Accept": {"application/json"},
			})
			_, reached, seen, logs := serve(t, p, req)
			if !reached {
				t.Fatal("the request was stopped; stripping must not reject")
			}
			for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "X-Http-Method-Override", "Forwarded"} {
				if seen.Get(h) != "" {
					t.Errorf("the handler saw %s; it should have been stripped", h)
				}
			}
			if seen.Get("Accept") != "application/json" {
				t.Error("Accept was removed; only the listed headers should be")
			}
			if len(logs) != 1 || logs[0]["msg"] != "request headers stripped" || logs[0]["level"] != "DEBUG" {
				t.Fatalf("logs = %v, want one DEBUG request headers stripped", logs)
			}
			for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "X-Http-Method-Override", "Forwarded"} {
				if !strings.Contains(logs[0]["headers"].(string), h) {
					t.Errorf("logged headers = %q, want it to name %s", logs[0]["headers"], h)
				}
			}
		})
	}
}

func TestNothingToStripLogsNothing(t *testing.T) {
	_, _, _, logs := serve(t, headerguard.DefaultPolicy(), get(nil))
	if len(logs) != 0 {
		t.Errorf("logs = %v, want none for an ordinary request", logs)
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]headerguard.Mode{"enforce": headerguard.Enforce, "report": headerguard.Report} {
		if got, err := headerguard.ParseMode(in); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := headerguard.ParseMode("off"); err == nil {
		t.Error(`ParseMode("off") = nil error, want one`)
	}
}
