package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRequestDeadline runs the whole service with a request that takes
// longer than REQUEST_TIMEOUT though no one call it makes times out: a
// conditional replace reads the item (600ms), then writes it, each within
// UPSTREAM_TIMEOUT (900ms), but not both within the deadline (1s). The
// client must get a 504 at the deadline, not at the write's own timeout
// (1.5s in), and the log must say so.
func TestRequestDeadline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			time.Sleep(600 * time.Millisecond)
		}
		if r.Method == http.MethodPut {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"widget","name":"Widget","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(t.Context())
	addr, logs, errc := startApp(t, ctx, map[string]string{
		"ADDR": "127.0.0.1:0", "UPSTREAM_URL": upstream.URL, "API_AUTH": "off",
		"UPSTREAM_TIMEOUT": "900ms", "REQUEST_TIMEOUT": "1s",
	})
	defer waitForShutdown(t, errc)
	defer cancel()

	resp, err := http.Get("http://" + addr + "/items/widget")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	etag := resp.Header.Get("ETag")

	logs.Reset()
	req, _ := http.NewRequest(http.MethodPut, "http://"+addr+"/items/widget", strings.NewReader(`{"name":"Gadget"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", etag)
	start := time.Now()
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("no response: %v", err)
	}
	resp.Body.Close()
	took := time.Since(start)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", resp.StatusCode)
	}
	if took < time.Second || took > 1300*time.Millisecond {
		t.Errorf("answered after %v, want about the 1s deadline", took)
	}
	var sent map[string]any
	for _, r := range logs.Records(t) {
		if r["msg"] == "response sent" {
			sent = r
		}
	}
	if sent == nil || sent["status"] != float64(504) || sent["response_code"] != "upstream_timeout" {
		t.Errorf("response sent = %v, want a 504, upstream_timeout", sent)
	}
}
