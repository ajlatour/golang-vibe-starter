package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// logLines parses each line of buf as a JSON log record.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// serveLogged runs one request through Middleware and a mux with handler on
// "GET /things/{id}", returning the response and the parsed log lines. A nil
// handler reports an internal error through ServerError.
func serveLogged(t *testing.T, handler http.HandlerFunc, req *http.Request) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&logs, nil)))
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			ServerError(logger, w, r, errors.New("store down"))
		}
	}
	mux := http.NewServeMux()
	mux.Handle("GET /things/{id}", handler)
	rec := httptest.NewRecorder()
	Middleware(logger, mux).ServeHTTP(rec, req)
	return rec, logLines(t, &logs)
}

func TestRequestIDGenerated(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) {
		if RequestID(r.Context()) == "" {
			t.Error("handler context has no request ID")
		}
	}
	rec, lines := serveLogged(t, ok, httptest.NewRequest(http.MethodGet, "/things/1", nil))

	id := rec.Header().Get(RequestIDHeader)
	if !validRequestID(id) {
		t.Fatalf("response %s = %q, want a generated ID", RequestIDHeader, id)
	}
	if got := lines[len(lines)-1][LogKeyRequestID]; got != id {
		t.Errorf("request log request_id = %v, want %q", got, id)
	}
}

func TestRequestIDFromClient(t *testing.T) {
	tests := []struct {
		name, sent string
		kept       bool
	}{
		{name: "uuid", sent: "3f2504e0-4f89-11d3-9a0c-0305e82c3301", kept: true},
		{name: "with separators", sent: "svc-a:req_42.1", kept: true},
		{name: "too long", sent: strings.Repeat("a", maxRequestIDLen+1)},
		{name: "spaces", sent: "has space"},
		{name: "quote", sent: `x"y`},
		{name: "newline", sent: "a\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/things/1", nil)
			req.Header.Set(RequestIDHeader, tt.sent)
			rec, _ := serveLogged(t, func(http.ResponseWriter, *http.Request) {}, req)

			got := rec.Header().Get(RequestIDHeader)
			if tt.kept && got != tt.sent {
				t.Errorf("response ID = %q, want the client's %q", got, tt.sent)
			}
			if !tt.kept && (got == tt.sent || !validRequestID(got)) {
				t.Errorf("response ID = %q, want a fresh generated ID", got)
			}
		})
	}
}

// TestLogSchema checks the field names shared by the request log and the
// internal-error log, and that both carry the same request_id.
func TestLogSchema(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/things/abc", nil)
	req.Header.Set(RequestIDHeader, "corr-1")
	_, lines := serveLogged(t, nil, req)
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2 (error, request): %v", len(lines), lines)
	}

	errLine, reqLine := lines[0], lines[1]
	if errLine["msg"] != "internal error" || errLine[LogKeyError] != "store down" {
		t.Errorf("error line = %v, want msg=internal error, error=store down", errLine)
	}
	if _, ok := errLine["err"]; ok {
		t.Error(`error line uses "err"; the schema key is "error"`)
	}
	for _, line := range lines {
		for key, want := range map[string]any{
			LogKeyRequestID: "corr-1",
			LogKeyMethod:    "GET",
			LogKeyRoute:     "GET /things/{id}",
			LogKeyPath:      "/things/abc",
		} {
			if line[key] != want {
				t.Errorf("%q line: %s = %v, want %v", line["msg"], key, line[key], want)
			}
		}
	}
	if _, ok := reqLine[LogKeyDurationMS].(float64); !ok {
		t.Errorf("request line duration_ms = %v (%T), want a number", reqLine[LogKeyDurationMS], reqLine[LogKeyDurationMS])
	}
	if _, ok := reqLine["duration"]; ok {
		t.Error(`request line has "duration"; the schema key is "duration_ms"`)
	}
}

func TestContextHandlerWithoutRequestID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&logs, nil))).With("service", "test")
	logger.InfoContext(t.Context(), "no request")
	line := logLines(t, &logs)[0]
	if _, ok := line[LogKeyRequestID]; ok {
		t.Errorf("line has request_id without a request: %v", line)
	}
	if line["service"] != "test" {
		t.Errorf("With attrs lost through the wrapper: %v", line)
	}
}

func TestNewLogger(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var logs bytes.Buffer
			logger := ServerConfig{LogFormat: format}.NewLogger(&logs, "api", "abc123")
			logger.InfoContext(WithRequestID(t.Context(), "r1"), "hello")
			out := logs.String()
			for _, want := range []string{"service", "api", "version", "abc123", "request_id", "r1"} {
				if !strings.Contains(out, want) {
					t.Errorf("log %q missing %q", out, want)
				}
			}
		})
	}
}

func TestBuildVersion(t *testing.T) {
	if BuildVersion() == "" {
		t.Error("BuildVersion() is empty")
	}
}
