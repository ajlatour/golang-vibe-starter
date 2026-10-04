package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// serveAt runs a request for path through Middleware with a debug-level
// logger and returns the single request log line.
func serveAt(t *testing.T, path string, status int) map[string]any {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	h := Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	lines := logLines(t, &logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want 1: %v", len(lines), lines)
	}
	return lines[0]
}

func TestRequestLogLevel(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		status int
		want   string
	}{
		{name: "success", path: "/items", status: 200, want: "INFO"},
		{name: "created", path: "/items", status: 201, want: "INFO"},
		{name: "client error is not an incident", path: "/items/x", status: 404, want: "INFO"},
		{name: "validation failure", path: "/items", status: 422, want: "INFO"},
		{name: "server error", path: "/items", status: 500, want: "ERROR"},
		{name: "bad gateway", path: "/items", status: 502, want: "ERROR"},
		{name: "healthcheck is debug", path: HealthzPath, status: 200, want: "DEBUG"},
		{name: "failing healthcheck is an error", path: HealthzPath, status: 503, want: "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := serveAt(t, tt.path, tt.status)
			if line["level"] != tt.want {
				t.Errorf("level = %v, want %s", line["level"], tt.want)
			}
		})
	}
}

// TestHealthzHiddenAtInfo checks the point of the debug level: the default
// info-level logger doesn't emit healthcheck requests at all.
func TestHealthzHiddenAtInfo(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Middleware(logger, http.HandlerFunc(Healthz))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, HealthzPath, nil))
	if logs.Len() != 0 {
		t.Errorf("healthcheck logged at info: %s", logs.String())
	}
}

func TestClientDisconnect(t *testing.T) {
	// The store fails because the client's request context was cancelled.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/things/1", nil).WithContext(ctx)

	rec, lines := serveLogged(t, func(w http.ResponseWriter, r *http.Request) {
		ServerError(slog.New(slog.DiscardHandler), w, r, fmt.Errorf("upstream GET /items: %w", context.Canceled))
	}, req)

	for _, l := range lines {
		if l["level"] == "ERROR" {
			t.Errorf("client disconnect logged at ERROR: %v", l)
		}
	}
	if rec.Code != StatusClientClosedRequest {
		t.Errorf("status = %d, want %d", rec.Code, StatusClientClosedRequest)
	}
	last := lines[len(lines)-1]
	if last["msg"] != "request" || last["status"] != float64(StatusClientClosedRequest) || last["level"] != "INFO" {
		t.Errorf("request log = %v, want INFO request with status 499", last)
	}
}

// TestCancelledWhileClientStillConnected checks that a context.Canceled
// error is only treated as a disconnect when the request's own context is
// cancelled; otherwise it's a real internal error.
func TestCancelledWhileClientStillConnected(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/things/1", nil) // context live
	ServerError(logger, rec, req, fmt.Errorf("internal: %w", context.Canceled))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if line := logLines(t, &logs)[0]; line["level"] != "ERROR" || line["msg"] != "internal error" {
		t.Errorf("log = %v, want ERROR internal error", line)
	}
}

func TestUpstreamTimeoutIsStillAnError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/things/1", nil)
	ServerError(logger, rec, req, fmt.Errorf("upstream: %w", context.DeadlineExceeded))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if line := logLines(t, &logs)[0]; line["level"] != "ERROR" {
		t.Errorf("log = %v, want ERROR", line)
	}
}
