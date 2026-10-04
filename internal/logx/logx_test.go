package logx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exact", 5, "exact"},
		{"abcdef", 3, "abc…"},
		{"héllo wörld", 5, "héllo…"}, // counts runes, never splits one
		{"", 3, ""},
	}
	for _, tt := range tests {
		if got := Truncate(tt.in, tt.n); got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

func TestRedactURLError(t *testing.T) {
	_, err := http.Get("http://alice:s3cret@127.0.0.1:1/v1/items")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	got := RedactURLError(fmt.Errorf("upstream: %w", err)).Error()
	if strings.Contains(got, "s3cret") || strings.Contains(got, "***") {
		t.Errorf("error %q still shows the password", got)
	}
	if !strings.Contains(got, "xxxxx") {
		t.Errorf("error %q, want the password shown as xxxxx like url.Redacted", got)
	}

	plain := errors.New("not a url error")
	if RedactURLError(plain) != plain {
		t.Error("RedactURLError changed an error that isn't a *url.Error")
	}
}

func TestContextHandlerWithoutRequestID(t *testing.T) {
	var logs logtest.Buffer
	logger := slog.New(NewContextHandler(logs.Handler(slog.LevelInfo))).With("service", "test")
	logger.InfoContext(t.Context(), "no request")
	line := logs.Records(t)[0]
	if _, ok := line[KeyRequestID]; ok {
		t.Errorf("line has request_id without a request: %v", line)
	}
	if line["service"] != "test" {
		t.Errorf("With attrs lost through the wrapper: %v", line)
	}
}

func TestBuildVersion(t *testing.T) {
	if BuildVersion() == "" {
		t.Error("BuildVersion() is empty")
	}
}

func TestNew(t *testing.T) {
	for _, format := range []string{"json", "text", ""} {
		t.Run("format "+format, func(t *testing.T) {
			var logs logtest.Buffer
			logger := New(&logs, slog.LevelInfo, format, "api", "abc123")
			logger.InfoContext(WithRequestID(t.Context(), "r1"), "hello")
			logger.Debug("below the level")

			out := logs.String()
			for _, want := range []string{"service", "api", "version", "abc123", "request_id", "r1", "hello"} {
				if !strings.Contains(out, want) {
					t.Errorf("log %q missing %q", out, want)
				}
			}
			if strings.Contains(out, "below the level") {
				t.Errorf("log %q includes a line below the level", out)
			}
			if format == "text" && strings.HasPrefix(out, "{") {
				t.Errorf("text format wrote JSON: %q", out)
			}
			if format != "text" && !strings.HasPrefix(out, "{") {
				t.Errorf("%q format didn't write JSON: %q", format, out)
			}
		})
	}
}

func TestRequestIDContext(t *testing.T) {
	if got := RequestID(t.Context()); got != "" {
		t.Errorf("RequestID(empty ctx) = %q, want empty", got)
	}
	if got := RequestID(WithRequestID(t.Context(), "abc")); got != "abc" {
		t.Errorf("RequestID = %q, want abc", got)
	}
}

type attrsErr struct{}

func (attrsErr) Error() string         { return "boom" }
func (attrsErr) LogAttrs() []slog.Attr { return []slog.Attr{slog.Int("extra", 7)} }

func TestErrorAttrs(t *testing.T) {
	plain := ErrorAttrs(errors.New("plain"))
	if len(plain) != 1 || plain[0].Key != KeyError {
		t.Errorf("ErrorAttrs(plain) = %v, want just the error", plain)
	}
	wrapped := ErrorAttrs(fmt.Errorf("outer: %w", attrsErr{}))
	if len(wrapped) != 2 || wrapped[0].Key != KeyError || wrapped[1].Key != "extra" {
		t.Errorf("ErrorAttrs(wrapped) = %v, want the error then the wrapped error's attrs", wrapped)
	}
}

func TestDurationAttr(t *testing.T) {
	a := DurationMS(1500 * 1000) // 1.5ms
	if a.Key != KeyDurationMS || a.Value.Float64() != 1.5 {
		t.Errorf("DurationMS(1.5ms) = %v, want %s=1.5", a, KeyDurationMS)
	}
	if k := DurationAttr(KeyUpstreamDurationMS, 0).Key; k != "upstream_duration_ms" {
		t.Errorf("DurationAttr key = %q", k)
	}
}
