package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
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

// noLimits is for tests about something else, which mustn't be throttled.
var noLimits = limit.Policy{}

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

	found := false
	for _, rec := range logs.Records(t) {
		if rec["msg"] == "using API keys" {
			found = rec["api_keys"] == "tests (write), reader (read)" && rec["reads"] == "required"
		}
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
