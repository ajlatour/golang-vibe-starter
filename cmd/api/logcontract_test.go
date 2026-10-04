package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
)

// logContract is the schema every log message must follow: its level and the
// fields it must carry. A message that isn't listed here fails the test, so
// adding a log line means deciding, and recording, what it promises.
// Keep it in step with the Logging section of the README.
//
// "request" is special-cased: its level depends on the response status.
var logContract = map[string]struct {
	level string
	keys  []string
}{
	// Lifecycle: logged outside any request, so no request_id.
	"using upstream":      {"INFO", []string{"url", "timeout"}},
	"using feature flags": {"INFO", []string{"url", "timeout"}},
	"server started":      {"INFO", []string{"addr"}},
	"shutting down":       {"INFO", []string{"timeout"}},
	"server stopped":      {"INFO", nil},

	// Logged while handling a request, so each carries request_id.
	"request":        {"", []string{"method", "route", "path", "status", "bytes", "duration_ms"}},
	"internal error": {"ERROR", []string{"method", "route", "path", "error"}},
	"upstream request": {"DEBUG", []string{
		"upstream", "upstream_status", "upstream_duration_ms"}},
	"upstream request failed": {"WARN", []string{
		"upstream", "upstream_duration_ms", "error"}},
	"flag service unavailable, using default flag values": {"WARN", []string{"flag", "error"}},
	"flag evaluation failed, using default":               {"DEBUG", []string{"flag", "error"}},
}

// outsideRequests lists the messages that legitimately have no request_id.
var outsideRequests = []string{
	"using upstream", "using feature flags", "server started", "shutting down", "server stopped",
}

// secrets are planted in request bodies, query strings, headers and URLs
// below. None may ever appear in a log line.
var secrets = []string{
	"BODYSECRET", "QUERYSECRET", "HEADERSECRET", "COOKIESECRET", "UPPASS", "FLAGPASS",
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestLogContract runs the whole service and drives it through success,
// client-error, upstream-failure and flag-outage paths, with secrets planted
// everywhere a careless log line might copy them from, then checks every
// line it wrote against the contract: required fields, levels, request_id,
// no forbidden field names, no secrets, bounded sizes.
func TestLogContract(t *testing.T) {
	// Upstream: the real item routes, except /items/boom, which fails with a
	// huge error message, and /items/dropme, which drops the connection.
	store := itemtest.NewMemStore()
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), store)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/items/boom" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": strings.Repeat("x", 5000)})
			return
		}
		if r.URL.Path == "/items/dropme" {
			// Close the connection mid-request: a transport error whose
			// message includes the upstream URL.
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		mux.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamURL.User = url.UserPassword("upuser", "UPPASS")

	// flagd: a dead address, so flag evaluation falls back and logs.
	dead := httptest.NewServer(http.NotFoundHandler())
	flagsURL, _ := url.Parse(dead.URL)
	dead.Close()
	flagsURL.User = url.UserPassword("flaguser", "FLAGPASS")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out lockedBuffer
	errc := make(chan error, 1)
	go func() {
		errc <- run(ctx, nil, getenvFrom(map[string]string{
			"ADDR":          "127.0.0.1:0",
			"UPSTREAM_URL":  upstreamURL.String(),
			"FLAGS_URL":     flagsURL.String(),
			"FLAGS_TIMEOUT": "200ms",
			"LOG_LEVEL":     "debug", // so debug lines are checked too
			"LOG_FORMAT":    "json",
		}), &out)
	}()
	addr := waitForAddr(t, &out, errc)
	base := "http://" + addr

	do := func(method, path, body string, hdr map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	secretHeaders := map[string]string{
		"Authorization":       "Bearer HEADERSECRET",
		"Cookie":              "session=COOKIESECRET",
		httpx.RequestIDHeader: "client-corr-1",
	}
	resp := do("GET", "/items?api_key=QUERYSECRET", "", secretHeaders)
	if got := resp.Header.Get(httpx.RequestIDHeader); got != "client-corr-1" {
		t.Errorf("response %s = %q, want the client's ID echoed", httpx.RequestIDHeader, got)
	}
	do("GET", "/items", "", nil) // a second list: flag evaluation falls back again
	do("POST", "/items?x=QUERYSECRET", `{"name":"BODYSECRET-ok","description":"BODYSECRET"}`, secretHeaders)
	do("POST", "/items", `{"name":"","description":"BODYSECRET"}`, nil) // 422
	do("POST", "/items", `{"name": BODYSECRET`, nil)                    // 400
	do("GET", "/items/missing", "", nil)                                // 404
	do("GET", "/items/boom", "", map[string]string{httpx.RequestIDHeader: "client-boom"})
	do("GET", "/items/dropme", "", nil) // transport error naming the credentialed URL
	do("GET", "/items/"+strings.Repeat("a", 3000), "", nil)
	do("GET", "/healthz", "", nil)

	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("service did not shut down")
	}

	raw := out.String()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	var recs []map[string]any
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", line, err)
		}
		recs = append(recs, m)

		for _, s := range secrets {
			if strings.Contains(line, s) {
				t.Errorf("log line contains secret %q: %.300s", s, line)
			}
		}
		// net/http masks a URL password as ***; a line showing that has an
		// unredacted URL in it (Redacted() writes xxxxx).
		if strings.Contains(line, "***") {
			t.Errorf("log line contains a URL that bypassed Redacted(): %.300s", line)
		}
		if len(line) > 4000 {
			t.Errorf("log line is %d bytes, want it bounded: %.200s...", len(line), line)
		}
		checkLine(t, m, line)
	}

	// Specific scenarios, found by their request_id.
	byID := map[string][]map[string]any{}
	for _, m := range recs {
		if id, _ := m[httpx.LogKeyRequestID].(string); id != "" {
			byID[id] = append(byID[id], m)
		}
	}
	if len(byID["client-corr-1"]) == 0 {
		t.Error("no log lines carry the client's correlation ID client-corr-1")
	}
	boom := msgs(byID["client-boom"])
	for _, want := range []string{"upstream request failed", "internal error", "request"} {
		if !slices.Contains(boom, want) {
			t.Errorf("failing upstream request logged %q, want it to include %q", boom, want)
		}
	}
	for _, m := range byID["client-boom"] {
		if m["msg"] == "internal error" && m["upstream_status"] != float64(503) {
			t.Errorf("internal error line = %v, want upstream_status=503", m)
		}
	}
	all := msgs(recs)
	for _, want := range []string{
		"flag service unavailable, using default flag values",
		"flag evaluation failed, using default",
		"upstream request",
	} {
		if !slices.Contains(all, want) {
			t.Errorf("no %q line logged; the scenario no longer exercises it", want)
		}
	}
}

// checkLine verifies one log record against the contract.
func checkLine(t *testing.T, m map[string]any, raw string) {
	t.Helper()
	msg, _ := m["msg"].(string)
	for _, k := range []string{"time", "level", "msg", "service", "version"} {
		if s, _ := m[k].(string); s == "" {
			t.Errorf("%q line has no %q: %.200s", msg, k, raw)
		}
	}
	if m["service"] != "api" {
		t.Errorf("%q line service = %v, want api", msg, m["service"])
	}
	for _, banned := range []string{"err", "duration"} {
		if _, ok := m[banned]; ok {
			t.Errorf("%q line has %q; the schema key is error / duration_ms: %.200s", msg, banned, raw)
		}
	}

	c, ok := logContract[msg]
	if !ok {
		t.Errorf("log message %q is not in logContract; add it with its level and required fields: %.200s", msg, raw)
		return
	}
	for _, k := range c.keys {
		if _, ok := m[k]; !ok {
			t.Errorf("%q line is missing %q: %.300s", msg, k, raw)
		}
	}

	level, _ := m["level"].(string)
	want := c.level
	if msg == "request" {
		status, _ := m["status"].(float64)
		switch {
		case status >= 500:
			want = "ERROR"
		case m["path"] == httpx.HealthzPath:
			want = "DEBUG"
		default:
			want = "INFO"
		}
	}
	if level != want {
		t.Errorf("%q line level = %s, want %s: %.200s", msg, level, want, raw)
	}

	id, _ := m[httpx.LogKeyRequestID].(string)
	if slices.Contains(outsideRequests, msg) {
		if id != "" {
			t.Errorf("%q line has request_id %q but isn't logged within a request", msg, id)
		}
	} else if id == "" {
		t.Errorf("%q line has no request_id: %.300s", msg, raw)
	}

	if p, _ := m["path"].(string); len([]rune(p)) > httpx.MaxLoggedPath+1 {
		t.Errorf("%q line path has %d runes, want at most %d", msg, len([]rune(p)), httpx.MaxLoggedPath+1)
	}
}

func msgs(recs []map[string]any) []string {
	var out []string
	for _, m := range recs {
		out = append(out, m["msg"].(string))
	}
	return out
}

// waitForAddr returns the address from the "server started" line in out.
func waitForAddr(t *testing.T, out *lockedBuffer, errc <-chan error) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errc:
			t.Fatalf("run() exited before starting: %v\nlogs: %s", err, out.String())
		default:
		}
		for _, line := range strings.Split(out.String(), "\n") {
			var m struct {
				Msg  string `json:"msg"`
				Addr string `json:"addr"`
			}
			if json.Unmarshal([]byte(line), &m) == nil && m.Msg == "server started" {
				return m.Addr
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not start; logs: %s", out.String())
	return ""
}
