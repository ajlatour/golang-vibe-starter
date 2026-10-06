package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
)

// The API keys the tests call the service with: one that may write, and one
// that may only read.
const (
	testWriteKey = "gvs_test-write-key-not-a-secret"
	testReadKey  = "gvs_test-read-key-not-a-secret"
)

// testAuth is the authentication the tests run the service with: the two
// test keys, with every route but the healthcheck needing one.
func testAuth(t testing.TB) apikey.Policy {
	t.Helper()
	keys, err := apikey.ParseKeys("tests:write:" + apikey.HashHex(testWriteKey) + ",reader:read:" + apikey.HashHex(testReadKey))
	if err != nil {
		t.Fatal(err)
	}
	return apikey.Policy{Keys: keys, Exempt: []string{httpx.HealthzPath}}
}

// noAuth is for tests about something else, which don't send a key.
var noAuth = apikey.Policy{Off: true}

// TestRunWithAPIKeys runs the whole service with keys configured: a request
// needs one, the healthcheck doesn't, and the startup line names the keys
// without ever logging one.
func TestRunWithAPIKeys(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	addr, logs, errc := startApp(t, ctx, map[string]string{
		"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://127.0.0.1:1",
		"API_KEYS": "tests:write:" + apikey.HashHex(testWriteKey) + ",reader:read:" + apikey.HashHex(testReadKey),
	})
	defer func() { cancel(); waitForShutdown(t, errc) }()

	get := func(path, key string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/items", ""); code != http.StatusUnauthorized {
		t.Errorf("no key: %d, want 401", code)
	}
	if code := get("/items", testReadKey); code != http.StatusBadGateway {
		t.Errorf("a valid key: %d, want it let through (to the dead upstream's 502)", code)
	}
	if code := get("/healthz", ""); code != http.StatusOK {
		t.Errorf("the healthcheck without a key: %d, want 200", code)
	}

	found, routed := false, false
	for _, rec := range logs.Records(t) {
		if rec["msg"] == "using API keys" {
			found = rec["api_keys"] == "tests (write), reader (read)" && rec["reads"] == "required"
		}
		// Authentication hands the router a copy of the request; the route
		// it matched must still reach the outer "response sent" line.
		if rec["msg"] == "response sent" && rec["path"] == "/items" && rec["status"] == float64(http.StatusBadGateway) {
			// ...and so must the name of the key that made it.
			routed = rec["route"] == "GET /items" && rec["api_key"] == "reader"
		}
	}
	if !routed {
		t.Errorf("the authenticated request's response sent line doesn't name its route and key:\n%s", logs)
	}
	if !found {
		t.Errorf("no startup line naming the keys:\n%s", logs)
	}
	for _, key := range []string{testWriteKey, testReadKey} {
		if strings.Contains(logs.String(), key) {
			t.Errorf("a key was logged:\n%s", logs)
		}
	}
}

func TestRunWithAuthenticationOffWarns(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	_, logs, errc := startApp(t, ctx, map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "API_AUTH": "off"})
	cancel()
	waitForShutdown(t, errc)
	if !strings.Contains(logs.String(), `"msg":"authentication is off: every request is allowed"`) {
		t.Errorf("no warning that authentication is off:\n%s", logs)
	}
}

// TestRunReloadsAPIKeysFile runs the service with API_KEYS_FILE and rotates a
// key while it runs: the new key works and the old one stops, with no
// restart.
func TestRunReloadsAPIKeysFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "keys")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("# the service's clients\nold:read:" + apikey.HashHex("gvs_old") + "\n")
	ctx, cancel := context.WithCancel(t.Context())
	addr, logs, errc := startApp(t, ctx, map[string]string{
		"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://127.0.0.1:1",
		"API_KEYS_FILE": file, "API_KEYS_RELOAD": "20ms",
	})
	defer func() { cancel(); waitForShutdown(t, errc) }()

	status := func(key string) int {
		req, _ := http.NewRequest("GET", "http://"+addr+"/items", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// 502 is the dead upstream: the key was let through.
	if got := status("gvs_old"); got != http.StatusBadGateway {
		t.Fatalf("the old key: %d, want it let through", got)
	}
	write("new:read:" + apikey.HashHex("gvs_new") + "\n")
	for deadline := time.Now().Add(5 * time.Second); status("gvs_new") != http.StatusBadGateway; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the new key was never accepted")
		}
	}
	if got := status("gvs_old"); got != http.StatusUnauthorized {
		t.Errorf("the old key after rotation: %d, want 401", got)
	}
	if !strings.Contains(logs.String(), `"msg":"API keys reloaded"`) {
		t.Errorf("no reload logged:\n%s", logs)
	}
}

// TestKeysAreRateLimitedClients checks the wiring between authentication and
// the per-client rate limit (identifyByKey): a key is one client whatever
// address it calls from, its own limit applies even when the policy sets
// none, and a key without one is limited by the policy, apart from others.
func TestKeysAreRateLimitedClients(t *testing.T) {
	const capped, open = "gvs_capped", "gvs_open"
	keys, err := apikey.ParseKeys("capped:read:" + apikey.HashHex(capped) + ":1/2,open:read:" + apikey.HashHex(open))
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(slog.New(slog.DiscardHandler), handlerConfig{
		Guard:  headerguard.DefaultPolicy(),
		Auth:   apikey.Policy{Keys: keys},
		Limits: limit.Policy{}, // no policy limit: only a key's own
	}, itemtest.NewMemStore())
	get := func(key string, port int) int {
		req := httptest.NewRequest(http.MethodGet, "/items", nil)
		req.RemoteAddr = fmt.Sprintf("192.0.2.1:%d", port)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	// The capped key's burst of 2, from two addresses, then refused.
	for i, want := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests} {
		if code := get(capped, 1000+i); code != want {
			t.Errorf("capped key, request %d: %d, want %d", i+1, code, want)
		}
	}
	for i := range 20 {
		if code := get(open, 2000); code != http.StatusOK {
			t.Fatalf("open key, request %d: %d, want 200: no limit applies to it", i+1, code)
		}
	}
}
