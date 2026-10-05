package featureflag_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/featureflag"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
)

func TestOFREPBool(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		def      bool // the configured default for "some-flag"
		want     bool
		wantWarn bool
	}{
		{name: "true", status: 200, body: `{"key":"k","value":true,"reason":"STATIC","variant":"on"}`, def: false, want: true},
		{name: "false overrides default", status: 200, body: `{"key":"k","value":false,"reason":"STATIC","variant":"off"}`, def: true, want: false},
		{name: "not found", status: 404, body: `{"key":"k","errorCode":"FLAG_NOT_FOUND","errorDetails":"flag not found"}`, def: true, want: true, wantWarn: true},
		{name: "server error", status: 500, body: `oops`, def: false, want: false, wantWarn: true},
		{name: "wrong type", status: 200, body: `{"key":"k","value":"yes"}`, def: false, want: false, wantWarn: true},
		{name: "malformed", status: 200, body: `{"value":`, def: true, want: true, wantWarn: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/ofrep/v1/evaluate/flags/some-flag" {
					t.Errorf("request = %s %s, want POST /ofrep/v1/evaluate/flags/some-flag", r.Method, r.URL.Path)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			var logs logtest.Buffer
			c := featureflag.NewOFREP(srv.URL+"/", srv.Client(), logs.Logger(slog.LevelInfo), featureflag.Defaults{"some-flag": tt.def})
			if got := c.Bool(t.Context(), "some-flag"); got != tt.want {
				t.Errorf("Bool() = %v, want %v", got, tt.want)
			}
			if warned := strings.Contains(logs.String(), `"level":"WARN"`); warned != tt.wantWarn {
				t.Errorf("warned = %v, want %v (logs: %s)", warned, tt.wantWarn, logs.String())
			}
		})
	}
}

func TestOFREPBoolUnreachable(t *testing.T) {
	c := featureflag.NewOFREP(nettest.DeadURL(), &http.Client{Timeout: time.Second}, slog.New(slog.DiscardHandler), featureflag.Defaults{"k": true})
	if got := c.Bool(t.Context(), "k"); !got {
		t.Error("Bool() = false, want the default (true) when flagd is unreachable")
	}
}

func TestOFREPBoolNoConfiguredDefault(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	var logs logtest.Buffer
	c := featureflag.NewOFREP(srv.URL, srv.Client(), logs.Logger(slog.LevelInfo), featureflag.Defaults{})
	if got := c.Bool(t.Context(), "undeclared"); got {
		t.Error("Bool() = true, want false for a flag with no configured default")
	}
	if !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("want an error-level log for the missing default, got: %s", logs.String())
	}
}

// flagdStub is a flagd stand-in whose answer tests can change.
type flagdStub struct {
	status atomic.Int32
	body   atomic.Value // string
	calls  atomic.Int32 // requests received
}

func newFlagdStub(t *testing.T) (*flagdStub, *httptest.Server) {
	t.Helper()
	s := &flagdStub{}
	s.set(200, `{"value":true}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		w.WriteHeader(int(s.status.Load()))
		_, _ = w.Write([]byte(s.body.Load().(string)))
	}))
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *flagdStub) set(status int, body string) {
	s.status.Store(int32(status))
	s.body.Store(body)
}

// TestOFREPOutageLogging checks that an outage produces one warning when it
// starts and one info line (with the fallback count) when it ends, with only
// debug lines in between.
func TestOFREPOutageLogging(t *testing.T) {
	stub, srv := newFlagdStub(t)
	var logs logtest.Buffer
	logger := logs.Logger(slog.LevelDebug)
	c := featureflag.NewOFREP(srv.URL, srv.Client(), logger, featureflag.Defaults{"f": false})

	if !c.Bool(t.Context(), "f") {
		t.Fatal("healthy Bool() = false, want true from flagd")
	}
	if len(logs.Records(t)) != 0 {
		t.Fatalf("healthy evaluation logged: %s", logs.String())
	}

	stub.set(503, `unavailable`)
	for range 3 {
		if c.Bool(t.Context(), "f") {
			t.Fatal("Bool() = true during outage, want the default (false)")
		}
	}
	got := logtest.LevelMsgs(logs.Records(t))
	want := []string{
		"WARN flag service unavailable, using default flag values",
		"DEBUG flag evaluation failed, using default",
		"DEBUG flag evaluation failed, using default",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("outage logs = %q, want %q", got, want)
	}

	logs.Reset()
	stub.set(200, `{"value":true}`)
	if !c.Bool(t.Context(), "f") {
		t.Fatal("recovered Bool() = false, want true")
	}
	recs := logs.Records(t)
	if len(recs) != 1 || recs[0]["level"] != "INFO" || recs[0]["msg"] != "flag service recovered" {
		t.Fatalf("recovery logs = %v, want one INFO flag service recovered", recs)
	}
	if recs[0]["fallbacks"] != float64(3) {
		t.Errorf("fallbacks = %v, want 3", recs[0]["fallbacks"])
	}
	if _, ok := recs[0]["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms = %v, want a number", recs[0]["duration_ms"])
	}

	// A second outage warns again.
	logs.Reset()
	stub.set(500, `oops`)
	c.Bool(t.Context(), "f")
	if got := logtest.LevelMsgs(logs.Records(t)); !slices.Equal(got, want[:1]) {
		t.Errorf("second outage logs = %q, want %q", got, want[:1])
	}
}

// TestOFREPFlagProblemLogging checks that a problem with one flag (flagd
// answers, but not usefully) warns once for that flag, and is not mistaken
// for an outage.
func TestOFREPFlagProblemLogging(t *testing.T) {
	stub, srv := newFlagdStub(t)
	stub.set(404, `{"errorCode":"FLAG_NOT_FOUND"}`)
	var logs logtest.Buffer
	logger := logs.Logger(slog.LevelDebug)
	c := featureflag.NewOFREP(srv.URL, srv.Client(), logger, featureflag.Defaults{"f": true, "g": false})

	for range 3 {
		c.Bool(t.Context(), "f")
	}
	c.Bool(t.Context(), "g") // a different flag warns on its own
	got := logtest.LevelMsgs(logs.Records(t))
	want := []string{
		"WARN flag evaluation failed, using default",
		"DEBUG flag evaluation failed, using default",
		"DEBUG flag evaluation failed, using default",
		"WARN flag evaluation failed, using default",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("logs = %q, want %q", got, want)
	}

	// Once the flag evaluates, a later failure warns again.
	stub.set(200, `{"value":true}`)
	c.Bool(t.Context(), "f")
	logs.Reset()
	stub.set(404, `{"errorCode":"FLAG_NOT_FOUND"}`)
	c.Bool(t.Context(), "f")
	if got := logtest.LevelMsgs(logs.Records(t)); len(got) != 1 || got[0] != want[0] {
		t.Errorf("logs after recovery = %q, want one WARN", got)
	}
}

func TestOFREPMissingDefaultLoggedOnce(t *testing.T) {
	stub, srv := newFlagdStub(t)
	stub.set(404, `{}`)
	var logs logtest.Buffer
	c := featureflag.NewOFREP(srv.URL, srv.Client(), logs.Logger(slog.LevelInfo), featureflag.Defaults{})
	for range 3 {
		c.Bool(t.Context(), "undeclared")
	}
	errors := 0
	for _, r := range logs.Records(t) {
		if r["level"] == "ERROR" {
			errors++
		}
	}
	if errors != 1 {
		t.Errorf("got %d ERROR lines, want 1:\n%s", errors, logs.String())
	}
}

// testClock is a clock tests move by hand.
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time      { return c.t }
func (c *testClock) add(d time.Duration) { c.t = c.t.Add(d) }
func newTestClock() *testClock           { return &testClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

// TestOFREPCache checks that WithCache answers from flagd's last value until
// the TTL is up, and only then asks again.
func TestOFREPCache(t *testing.T) {
	stub, srv := newFlagdStub(t)
	clock := newTestClock()
	c := featureflag.NewOFREP(srv.URL, srv.Client(), slog.New(slog.DiscardHandler), featureflag.Defaults{"f": false},
		featureflag.WithCache(time.Second))
	featureflag.SetClock(c, clock.now)

	if !c.Bool(t.Context(), "f") {
		t.Fatal("Bool() = false, want true from flagd")
	}
	stub.set(200, `{"value":false}`)
	clock.add(999 * time.Millisecond)
	if !c.Bool(t.Context(), "f") {
		t.Error("Bool() within the TTL = false, want the cached true")
	}
	if n := stub.calls.Load(); n != 1 {
		t.Errorf("flagd asked %d times within the TTL, want 1", n)
	}
	clock.add(time.Millisecond)
	if c.Bool(t.Context(), "f") {
		t.Error("Bool() after the TTL = true, want flagd's new false")
	}
	if n := stub.calls.Load(); n != 2 {
		t.Errorf("flagd asked %d times, want 2", n)
	}
}

// TestOFREPCacheKeepsNoFailures checks a flag that failed to evaluate isn't
// cached: the next evaluation asks flagd again.
func TestOFREPCacheKeepsNoFailures(t *testing.T) {
	stub, srv := newFlagdStub(t)
	stub.set(404, `{"errorCode":"FLAG_NOT_FOUND"}`)
	c := featureflag.NewOFREP(srv.URL, srv.Client(), slog.New(slog.DiscardHandler), featureflag.Defaults{"f": false},
		featureflag.WithCache(time.Minute))
	featureflag.SetClock(c, newTestClock().now)

	c.Bool(t.Context(), "f")
	stub.set(200, `{"value":true}`)
	if !c.Bool(t.Context(), "f") {
		t.Error("Bool() = false, want flagd's true: a failed evaluation was cached")
	}
}

// TestOFREPOutageBackoff checks that during an outage, WithCache asks flagd at
// most once per TTL, answering the evaluations in between from the defaults
// without a request, and counts them all as fallbacks when flagd recovers.
func TestOFREPOutageBackoff(t *testing.T) {
	stub, srv := newFlagdStub(t)
	stub.set(503, `unavailable`)
	clock := newTestClock()
	var logs logtest.Buffer
	c := featureflag.NewOFREP(srv.URL, srv.Client(), logs.Logger(slog.LevelDebug), featureflag.Defaults{"f": true, "g": false},
		featureflag.WithCache(time.Second))
	featureflag.SetClock(c, clock.now)

	for range 5 {
		if !c.Bool(t.Context(), "f") || c.Bool(t.Context(), "g") {
			t.Fatal("Bool() during an outage didn't give the defaults")
		}
	}
	if n := stub.calls.Load(); n != 1 {
		t.Errorf("flagd asked %d times within the TTL of an outage, want 1", n)
	}

	clock.add(time.Second)
	c.Bool(t.Context(), "f") // the one retry, still failing
	c.Bool(t.Context(), "f")
	if n := stub.calls.Load(); n != 2 {
		t.Errorf("flagd asked %d times after one TTL, want 2", n)
	}

	stub.set(200, `{"value":false}`)
	clock.add(time.Second)
	if c.Bool(t.Context(), "f") {
		t.Error("Bool() after recovery = true, want flagd's false")
	}
	got := logtest.LevelMsgs(logs.Records(t))
	if len(got) < 2 || got[0] != "WARN flag service unavailable, using default flag values" || got[len(got)-1] != "INFO flag service recovered" {
		t.Fatalf("logs = %q, want one WARN at the start and one INFO at the end", got)
	}
	for _, m := range got[1 : len(got)-1] {
		if !strings.HasPrefix(m, "DEBUG ") {
			t.Errorf("log during the outage = %q, want debug only", m)
		}
	}
	last := logs.Records(t)[len(got)-1]
	if last["fallbacks"] != float64(12) {
		t.Errorf("fallbacks = %v, want 12: every evaluation that took a default", last["fallbacks"])
	}
}

// TestOFREPWithoutCacheAsksEveryTime checks the default, no cache: during an
// outage every evaluation asks flagd, as before WithCache existed.
func TestOFREPWithoutCacheAsksEveryTime(t *testing.T) {
	stub, srv := newFlagdStub(t)
	c := featureflag.NewOFREP(srv.URL, srv.Client(), slog.New(slog.DiscardHandler), featureflag.Defaults{"f": false})
	for range 3 {
		c.Bool(t.Context(), "f")
	}
	stub.set(503, `unavailable`)
	for range 3 {
		c.Bool(t.Context(), "f")
	}
	if n := stub.calls.Load(); n != 6 {
		t.Errorf("flagd asked %d times, want 6", n)
	}
}
