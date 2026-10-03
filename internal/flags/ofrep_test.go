package flags_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
