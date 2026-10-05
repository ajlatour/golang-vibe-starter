package apikey_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

const (
	writeKey = "gvs_write-key-for-tests-only-aaaaaaaaaaaaaaaaaaaa"
	readKey  = "gvs_read-key-for-tests-only-bbbbbbbbbbbbbbbbbbbbbb"
)

func policy(t *testing.T) apikey.Policy {
	t.Helper()
	keys, err := apikey.ParseKeys("ci:write:" + apikey.HashHex(writeKey) + ", dash:read:" + apikey.HashHex(readKey))
	if err != nil {
		t.Fatal(err)
	}
	return apikey.Policy{Keys: keys, Exempt: []string{"/healthz"}}
}

// serve sends a request through the middleware and returns the response,
// the key name the handler saw ("-" if it didn't run) and the log records.
func serve(t *testing.T, p apikey.Policy, method, path, authorization string) (*httptest.ResponseRecorder, string, []map[string]any) {
	t.Helper()
	var logs logtest.Buffer
	saw := "-"
	h := apikey.Middleware(logs.Logger(slog.LevelDebug), p, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = apikey.Name(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(method, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, saw, logs.Records(t)
}

func TestMiddleware(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, auth string
		openReads                bool
		want                     int
		wantKey, wantReason      string
	}{
		{name: "write key writes", method: "POST", path: "/items", auth: "Bearer " + writeKey, want: 204, wantKey: "ci"},
		{name: "write key reads", method: "GET", path: "/items", auth: "Bearer " + writeKey, want: 204, wantKey: "ci"},
		{name: "read key reads", method: "GET", path: "/items", auth: "Bearer " + readKey, want: 204, wantKey: "dash"},
		{name: "scheme is case-insensitive", method: "GET", path: "/items", auth: "bearer " + readKey, want: 204, wantKey: "dash"},
		{name: "read key can't write", method: "DELETE", path: "/items/x", auth: "Bearer " + readKey, want: 403, wantReason: "read-only key"},
		{name: "no key", method: "GET", path: "/items", want: 401, wantReason: "missing key"},
		{name: "unknown key", method: "GET", path: "/items", auth: "Bearer gvs_nope", want: 401, wantReason: "unknown key"},
		{name: "the hash isn't the key", method: "GET", path: "/items", auth: "Bearer " + apikey.HashHex(writeKey), want: 401, wantReason: "unknown key"},
		{name: "basic auth", method: "GET", path: "/items", auth: "Basic " + writeKey, want: 401, wantReason: "not a bearer token"},
		{name: "empty bearer", method: "GET", path: "/items", auth: "Bearer ", want: 401, wantReason: "not a bearer token"},
		{name: "healthcheck is exempt", method: "GET", path: "/healthz", want: 204, wantKey: ""},
		{name: "open reads: no key reads", method: "GET", path: "/items", openReads: true, want: 204, wantKey: ""},
		{name: "open reads: no key can't write", method: "POST", path: "/items", openReads: true, want: 401, wantReason: "missing key"},
		{name: "open reads: a wrong key is still wrong", method: "GET", path: "/items", auth: "Bearer gvs_nope", openReads: true, want: 401, wantReason: "unknown key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := policy(t)
			p.OpenReads = tt.openReads
			rec, saw, logs := serve(t, p, tt.method, tt.path, tt.auth)
			if rec.Code != tt.want {
				t.Fatalf("status %d, want %d", rec.Code, tt.want)
			}
			if tt.want == 204 {
				if saw != tt.wantKey {
					t.Errorf("the handler saw key %q, want %q", saw, tt.wantKey)
				}
				if len(logs) != 0 {
					t.Errorf("logged %v for an allowed request", logs)
				}
				return
			}
			if saw != "-" {
				t.Error("the handler ran for a refused request")
			}
			if !strings.HasPrefix(rec.Body.String(), `{"error":`) {
				t.Errorf("body %q, want a JSON error", rec.Body)
			}
			if (tt.want == 401) != (rec.Header().Get("WWW-Authenticate") != "") {
				t.Errorf("WWW-Authenticate = %q on a %d", rec.Header().Get("WWW-Authenticate"), tt.want)
			}
			if len(logs) != 1 || logs[0]["msg"] != "request refused by authentication" || logs[0]["reason"] != tt.wantReason || logs[0]["status"] != float64(tt.want) {
				t.Errorf("logs = %v, want one refusal with reason %q", logs, tt.wantReason)
			}
			for _, line := range logs {
				for _, v := range line {
					if s, ok := v.(string); ok && (strings.Contains(s, writeKey) || strings.Contains(s, readKey)) {
						t.Errorf("a key was logged: %v", line)
					}
				}
			}
		})
	}
}

func TestOff(t *testing.T) {
	rec, _, _ := serve(t, apikey.Policy{Off: true}, "DELETE", "/items/x", "")
	if rec.Code != 204 {
		t.Errorf("status %d with authentication off, want 204", rec.Code)
	}
}

func TestValidate(t *testing.T) {
	if err := (apikey.Policy{}).Validate(); err == nil {
		t.Error("no keys and not off: want an error")
	}
	if err := (apikey.Policy{Off: true}).Validate(); err != nil {
		t.Errorf("off: %v", err)
	}
	if err := policy(t).Validate(); err != nil {
		t.Errorf("with keys: %v", err)
	}
}

func TestParseKeys(t *testing.T) {
	h := apikey.HashHex("k")
	for _, bad := range []string{
		"ci:write",               // too few fields
		"ci:admin:" + h,          // unknown scope
		"CI:write:" + h,          // upper case name
		"ci:write:" + h[:63],     // short hash
		"ci:write:gvs_plaintext", // the key itself
		"ci:write:" + h + ",ci:read:" + h,
	} {
		if _, err := apikey.ParseKeys(bad); err == nil {
			t.Errorf("ParseKeys(%q) = nil error, want one", bad)
		}
	}
	keys, err := apikey.ParseKeys(" a:read:" + h + " ,, b:write:" + h + ",")
	if err != nil || len(keys) != 2 || keys[0].Name != "a" || keys[1].Scope != apikey.Write {
		t.Errorf("ParseKeys = %+v, %v", keys, err)
	}
}

func TestGenerate(t *testing.T) {
	key, entry, err := apikey.Generate("ci", apikey.Write)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "gvs_") || len(key) < 40 {
		t.Errorf("key %q: want gvs_ and 256 random bits", key)
	}
	keys, err := apikey.ParseKeys(entry)
	if err != nil || len(keys) != 1 || keys[0].Name != "ci" || keys[0].Scope != apikey.Write {
		t.Fatalf("the generated entry %q doesn't parse back: %+v, %v", entry, keys, err)
	}
	rec, saw, _ := serve(t, apikey.Policy{Keys: keys}, "POST", "/items", "Bearer "+key)
	if rec.Code != 204 || saw != "ci" {
		t.Errorf("the generated key was refused: %d", rec.Code)
	}
	if other, _, _ := apikey.Generate("ci", apikey.Write); other == key {
		t.Error("two generated keys are the same")
	}
}
