package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

// serveLogged runs one request through Middleware and a mux with handler on
// "GET /things/{id}", returning the response and the parsed log lines. A nil
// handler reports an internal error through ServerError.
func serveLogged(t *testing.T, handler http.HandlerFunc, req *http.Request) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()
	var logs logtest.Buffer
	logger := slog.New(NewContextHandler(logs.Handler(slog.LevelInfo)))
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			ServerError(logger, w, r, errors.New("store down"))
		}
	}
	mux := http.NewServeMux()
	mux.Handle("GET /things/{id}", handler)
	rec := httptest.NewRecorder()
	Middleware(logger, mux).ServeHTTP(rec, req)
	return rec, logs.Records(t)
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
	if len(lines) != 3 {
		t.Fatalf("got %d log lines, want 3 (request received, internal error, response sent): %v", len(lines), lines)
	}

	recvLine, errLine, reqLine := lines[0], lines[1], lines[2]
	if recvLine["msg"] != "request received" || reqLine["msg"] != "response sent" {
		t.Errorf("messages = %v, %v, want request received ... response sent", recvLine["msg"], reqLine["msg"])
	}
	if recvLine[LogKeyMethod] != "GET" || recvLine[LogKeyPath] != "/things/abc" || recvLine[LogKeyRequestID] != "corr-1" {
		t.Errorf("request received line = %v, want method, path and request_id", recvLine)
	}
	if _, ok := recvLine[LogKeyContentLength].(float64); !ok {
		t.Errorf("request received line content_length = %v, want a number", recvLine[LogKeyContentLength])
	}
	if _, ok := recvLine[LogKeyRoute]; ok {
		t.Error("request received line has a route; ServeMux hasn't matched one yet")
	}
	if errLine["msg"] != "internal error" || errLine[LogKeyError] != "store down" {
		t.Errorf("error line = %v, want msg=internal error, error=store down", errLine)
	}
	if _, ok := errLine["err"]; ok {
		t.Error(`error line uses "err"; the schema key is "error"`)
	}
	for _, line := range lines[1:] {
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
	var logs logtest.Buffer
	logger := slog.New(NewContextHandler(logs.Handler(slog.LevelInfo))).With("service", "test")
	logger.InfoContext(t.Context(), "no request")
	line := logs.Records(t)[0]
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
			var logs logtest.Buffer
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
// logger and returns its "request received" and "response sent" lines.
func serveAt(t *testing.T, path string, status int) (received, sent map[string]any) {
	t.Helper()
	var logs logtest.Buffer
	logger := slog.New(NewContextHandler(logs.Handler(slog.LevelDebug)))
	h := Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	lines := logs.Records(t)
	if len(lines) != 2 || lines[0]["msg"] != "request received" || lines[1]["msg"] != "response sent" {
		t.Fatalf("got log lines %v, want request received then response sent", lines)
	}
	return lines[0], lines[1]
}

func TestRequestLogLevel(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		status       int
		wantReceived string
		wantSent     string
	}{
		{name: "success", path: "/items", status: 200, wantReceived: "INFO", wantSent: "INFO"},
		{name: "created", path: "/items", status: 201, wantReceived: "INFO", wantSent: "INFO"},
		{name: "client error is not an incident", path: "/items/x", status: 404, wantReceived: "INFO", wantSent: "INFO"},
		{name: "validation failure", path: "/items", status: 422, wantReceived: "INFO", wantSent: "INFO"},
		{name: "server error", path: "/items", status: 500, wantReceived: "INFO", wantSent: "ERROR"},
		{name: "bad gateway", path: "/items", status: 502, wantReceived: "INFO", wantSent: "ERROR"},
		{name: "healthcheck is debug", path: HealthzPath, status: 200, wantReceived: "DEBUG", wantSent: "DEBUG"},
		{name: "failing healthcheck is an error", path: HealthzPath, status: 503, wantReceived: "DEBUG", wantSent: "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received, sent := serveAt(t, tt.path, tt.status)
			if received["level"] != tt.wantReceived {
				t.Errorf("request received level = %v, want %s", received["level"], tt.wantReceived)
			}
			if sent["level"] != tt.wantSent {
				t.Errorf("response sent level = %v, want %s", sent["level"], tt.wantSent)
			}
		})
	}
}

// TestHealthzHiddenAtInfo checks the point of the debug level: the default
// info-level logger doesn't emit healthcheck requests at all.
func TestHealthzHiddenAtInfo(t *testing.T) {
	var logs logtest.Buffer
	logger := slog.New(logs.Handler(slog.LevelInfo))
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
	if last["msg"] != "response sent" || last["status"] != float64(StatusClientClosedRequest) || last["level"] != "INFO" {
		t.Errorf("request log = %v, want INFO response sent with status 499", last)
	}
}

// TestCancelledWhileClientStillConnected checks that a context.Canceled
// error is only treated as a disconnect when the request's own context is
// cancelled; otherwise it's a real internal error.
func TestCancelledWhileClientStillConnected(t *testing.T) {
	var logs logtest.Buffer
	logger := slog.New(logs.Handler(slog.LevelInfo))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/things/1", nil) // context live
	ServerError(logger, rec, req, fmt.Errorf("internal: %w", context.Canceled))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if line := logs.Records(t)[0]; line["level"] != "ERROR" || line["msg"] != "internal error" {
		t.Errorf("log = %v, want ERROR internal error", line)
	}
}

func TestUpstreamTimeoutIsStillAnError(t *testing.T) {
	var logs logtest.Buffer
	logger := slog.New(logs.Handler(slog.LevelInfo))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/things/1", nil)
	ServerError(logger, rec, req, fmt.Errorf("upstream: %w", context.DeadlineExceeded))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if line := logs.Records(t)[0]; line["level"] != "ERROR" {
		t.Errorf("log = %v, want ERROR", line)
	}
}

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

func TestLongPathIsTruncatedInRequestLog(t *testing.T) {
	long := "/items/" + strings.Repeat("a", 5000)
	received, sent := serveAt(t, long, 200)
	for _, line := range []map[string]any{received, sent} {
		path, _ := line[LogKeyPath].(string)
		if n := len([]rune(path)); n > MaxLoggedPath+1 {
			t.Errorf("%v line path has %d runes, want at most %d", line["msg"], n, MaxLoggedPath+1)
		}
	}
}
