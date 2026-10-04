package flags_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/flags"
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

			var logs bytes.Buffer
			c := flags.NewOFREP(srv.URL+"/", srv.Client(), slog.New(slog.NewTextHandler(&logs, nil)), flags.Defaults{"some-flag": tt.def})
			if got := c.Bool(t.Context(), "some-flag"); got != tt.want {
				t.Errorf("Bool() = %v, want %v", got, tt.want)
			}
			if warned := strings.Contains(logs.String(), "level=WARN"); warned != tt.wantWarn {
				t.Errorf("warned = %v, want %v (logs: %s)", warned, tt.wantWarn, logs.String())
			}
		})
	}
}

func TestOFREPBoolUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening any more

	c := flags.NewOFREP(url, &http.Client{Timeout: time.Second}, slog.New(slog.DiscardHandler), flags.Defaults{"k": true})
	if got := c.Bool(t.Context(), "k"); !got {
		t.Error("Bool() = false, want the default (true) when flagd is unreachable")
	}
}

func TestOFREPBoolNoConfiguredDefault(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	var logs bytes.Buffer
	c := flags.NewOFREP(srv.URL, srv.Client(), slog.New(slog.NewTextHandler(&logs, nil)), flags.Defaults{})
	if got := c.Bool(t.Context(), "undeclared"); got {
		t.Error("Bool() = true, want false for a flag with no configured default")
	}
	if !strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("want an error-level log for the missing default, got: %s", logs.String())
	}
}

// flagdStub is a flagd stand-in whose answer tests can change.
type flagdStub struct {
	status atomic.Int32
	body   atomic.Value // string
}

func newFlagdStub(t *testing.T) (*flagdStub, *httptest.Server) {
	t.Helper()
	s := &flagdStub{}
	s.set(200, `{"value":true}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// logRecords parses the JSON log lines in buf.
func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func levels(recs []map[string]any) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r["level"].(string)+" "+r["msg"].(string))
	}
	return out
}

// TestOFREPOutageLogging checks that an outage produces one warning when it
// starts and one info line (with the fallback count) when it ends, with only
// debug lines in between.
func TestOFREPOutageLogging(t *testing.T) {
	stub, srv := newFlagdStub(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := flags.NewOFREP(srv.URL, srv.Client(), logger, flags.Defaults{"f": false})

	if !c.Bool(t.Context(), "f") {
		t.Fatal("healthy Bool() = false, want true from flagd")
	}
	if len(logRecords(t, &logs)) != 0 {
		t.Fatalf("healthy evaluation logged: %s", logs.String())
	}

	stub.set(503, `unavailable`)
	for range 3 {
		if c.Bool(t.Context(), "f") {
			t.Fatal("Bool() = true during outage, want the default (false)")
		}
	}
	got := levels(logRecords(t, &logs))
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
	recs := logRecords(t, &logs)
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
	if got := levels(logRecords(t, &logs)); !slices.Equal(got, want[:1]) {
		t.Errorf("second outage logs = %q, want %q", got, want[:1])
	}
}

// TestOFREPFlagProblemLogging checks that a problem with one flag (flagd
// answers, but not usefully) warns once for that flag, and is not mistaken
// for an outage.
func TestOFREPFlagProblemLogging(t *testing.T) {
	stub, srv := newFlagdStub(t)
	stub.set(404, `{"errorCode":"FLAG_NOT_FOUND"}`)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := flags.NewOFREP(srv.URL, srv.Client(), logger, flags.Defaults{"f": true, "g": false})

	for range 3 {
		c.Bool(t.Context(), "f")
	}
	c.Bool(t.Context(), "g") // a different flag warns on its own
	got := levels(logRecords(t, &logs))
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
	if got := levels(logRecords(t, &logs)); len(got) != 1 || got[0] != want[0] {
		t.Errorf("logs after recovery = %q, want one WARN", got)
	}
}

func TestOFREPMissingDefaultLoggedOnce(t *testing.T) {
	stub, srv := newFlagdStub(t)
	stub.set(404, `{}`)
	var logs bytes.Buffer
	c := flags.NewOFREP(srv.URL, srv.Client(), slog.New(slog.NewJSONHandler(&logs, nil)), flags.Defaults{})
	for range 3 {
		c.Bool(t.Context(), "undeclared")
	}
	errors := 0
	for _, r := range logRecords(t, &logs) {
		if r["level"] == "ERROR" {
			errors++
		}
	}
	if errors != 1 {
		t.Errorf("got %d ERROR lines, want 1:\n%s", errors, logs.String())
	}
}
