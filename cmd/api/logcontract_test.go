package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpapi"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
)

// secrets are planted in request bodies, query strings, headers and URLs
// below. None may ever appear in a log line.
var secrets = []string{
	"BODYSECRET", "QUERYSECRET", "HEADERSECRET", "COOKIESECRET", "UPPASS", "FLAGPASS", "FWDSECRET",
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
	httpapi.RegisterRoutes(mux, slog.New(slog.DiscardHandler), store)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/items/boom" {
			w.Header().Set("Content-Type", "application/json")
			nettest.Status(http.StatusServiceUnavailable, `{"error":"`+strings.Repeat("x", 5000)+`"}`)(w, r)
			return
		}
		if r.URL.Path == "/items/dropme" {
			// Close the connection mid-request: a transport error whose
			// message includes the upstream URL.
			nettest.Drop(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamURL.User = url.UserPassword("upuser", "UPPASS")

	// flagd: a dead address, so flag evaluation falls back and logs.
	flagsURL, _ := url.Parse(nettest.DeadURL())
	flagsURL.User = url.UserPassword("flaguser", "FLAGPASS")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	addr, out, errc := startApp(t, ctx, map[string]string{
		"ADDR":          "127.0.0.1:0",
		"UPSTREAM_URL":  upstreamURL.String(),
		"FLAGS_URL":     flagsURL.String(),
		"FLAGS_TIMEOUT": "200ms",
		"LOG_LEVEL":     "debug", // so debug lines are checked too
		"LOG_FORMAT":    "json",
	})
	base := "http://" + addr

	do := func(method, path, body string, hdr map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
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

	// Headers a careless logger might copy, with a distinct correlation ID
	// per request so each request's lines can be told apart.
	secretHeaders := func(id string) map[string]string {
		return map[string]string{
			"Authorization":       "Bearer HEADERSECRET",
			"Cookie":              "session=COOKIESECRET",
			httpx.RequestIDHeader: id,
		}
	}
	resp := do("GET", "/items?api_key=QUERYSECRET", "", secretHeaders("client-corr-1"))
	if got := resp.Header.Get(httpx.RequestIDHeader); got != "client-corr-1" {
		t.Errorf("response %s = %q, want the client's ID echoed", httpx.RequestIDHeader, got)
	}
	do("GET", "/items", "", nil) // a second list: flag evaluation falls back again
	do("POST", "/items?x=QUERYSECRET", `{"name":"BODYSECRET-ok","description":"BODYSECRET"}`, secretHeaders("client-corr-2"))
	do("POST", "/items", `{"name":"","description":"BODYSECRET"}`, nil) // 422
	do("POST", "/items", `{"name": BODYSECRET`, nil)                    // 400
	do("GET", "/items/missing", "", nil)                                // 404
	do("GET", "/items/boom", "", map[string]string{httpx.RequestIDHeader: "client-boom"})
	do("GET", "/items/dropme", "", nil) // transport error naming the credentialed URL
	do("GET", "/items/"+strings.Repeat("a", 3000), "", nil)
	do("GET", "/healthz", "", nil)
	// Header screening: a body that isn't JSON is rejected, and a proxy header
	// is stripped (its value, a secret, must not be logged; only its name).
	do("POST", "/items", `{"name":"x"}`, map[string]string{"Content-Type": "text/plain"})
	do("GET", "/items", "", map[string]string{"X-Forwarded-For": "FWDSECRET"})

	cancel()
	waitForShutdown(t, errc)

	var recs []map[string]any
	for _, line := range out.Lines() {
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
		if id, _ := m[logx.KeyRequestID].(string); id != "" {
			byID[id] = append(byID[id], m)
		}
	}
	if len(byID["client-corr-1"]) == 0 {
		t.Error("no log lines carry the client's correlation ID client-corr-1")
	}
	// The full story of one failing request, in order: it arrives, the
	// upstream call goes out, the upstream's 503 comes back, the call is
	// reported failed, the handler logs the error, and the 502 goes out.
	wantBoom := []string{
		"request received", "upstream request sent", "upstream response received",
		"upstream request failed", "internal error", "response sent",
	}
	if got := logtest.Msgs(byID["client-boom"]); !slices.Equal(got, wantBoom) {
		t.Errorf("failing request logged %q, want %q", got, wantBoom)
	}
	checkPairing(t, byID)

	// The client is told the upstream failed (502), not that the API did (500).
	for _, m := range byID["client-boom"] {
		if m["msg"] == "response sent" && m["status"] != float64(http.StatusBadGateway) {
			t.Errorf("response sent for a failing upstream has status %v, want 502", m["status"])
		}
	}
	for _, m := range byID["client-boom"] {
		if m["msg"] == "internal error" && m["upstream_status"] != float64(503) {
			t.Errorf("internal error line = %v, want upstream_status=503", m)
		}
	}
	all := logtest.Msgs(recs)
	for _, want := range []string{
		"flag service unavailable, using default flag values",
		"flag evaluation failed, using default",
		"upstream request sent",
		"upstream response received",
		"request headers rejected",
		"request headers stripped",
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

	c, ok := logtest.Contract[msg]
	if !ok {
		t.Errorf("log message %q is not in logtest.Contract; add it with its levels and required fields: %.200s", msg, raw)
		return
	}
	for _, k := range c.Keys {
		if _, ok := m[k]; !ok {
			t.Errorf("%q line is missing %q: %.300s", msg, k, raw)
		}
	}

	level, _ := m["level"].(string)
	if !slices.Contains(c.Levels, level) {
		t.Errorf("%q line level = %s, want one of %v: %.200s", msg, level, c.Levels, raw)
	}
	// Two messages have a precise level rule, beyond the set the contract allows.
	switch msg {
	case "request received":
		want := "INFO"
		if m["path"] == httpx.HealthzPath {
			want = "DEBUG"
		}
		if level != want {
			t.Errorf("%q line level = %s, want %s", msg, level, want)
		}
	case "response sent":
		status, _ := m["status"].(float64)
		want := "INFO"
		switch {
		case status >= 500:
			want = "ERROR"
		case m["path"] == httpx.HealthzPath:
			want = "DEBUG"
		}
		if level != want {
			t.Errorf("%q line level = %s, want %s (status %v)", msg, level, want, status)
		}
	}

	id, _ := m[logx.KeyRequestID].(string)
	if slices.Contains(logtest.OutsideRequests, msg) {
		if id != "" {
			t.Errorf("%q line has request_id %q but isn't logged within a request", msg, id)
		}
	} else if id == "" {
		t.Errorf("%q line has no request_id: %.300s", msg, raw)
	}

	if p, _ := m["path"].(string); len([]rune(p)) > logx.MaxLoggedPath+1 {
		t.Errorf("%q line path has %d runes, want at most %d", msg, len([]rune(p)), logx.MaxLoggedPath+1)
	}
}

// checkPairing verifies, for every request, that its events are complete and
// ordered: it opens with "request received" and closes with "response sent",
// each exactly once, and every "upstream request sent" is answered by exactly
// one outcome, a response or (when none arrived) a failure.
func checkPairing(t *testing.T, byID map[string][]map[string]any) {
	t.Helper()
	for id, recs := range byID {
		got := logtest.Msgs(recs)
		if got[0] != "request received" || got[len(got)-1] != "response sent" {
			t.Errorf("request %s: lines %q, want first request received and last response sent", id, got)
		}
		count := func(msg string) int {
			n := 0
			for _, m := range got {
				if m == msg {
					n++
				}
			}
			return n
		}
		if count("request received") != 1 || count("response sent") != 1 {
			t.Errorf("request %s: want exactly one request received and one response sent, got %q", id, got)
		}

		// An outcome is a response, or a failure that has no response.
		outcomes := count("upstream response received")
		for _, m := range recs {
			msg := m["msg"]
			if (msg == "upstream request failed" || msg == "upstream request cancelled") && m["upstream_status"] == nil {
				outcomes++
			}
		}
		if sent := count("upstream request sent"); sent != outcomes {
			t.Errorf("request %s: %d upstream requests sent but %d outcomes: %q", id, sent, outcomes, got)
		}
	}
}
