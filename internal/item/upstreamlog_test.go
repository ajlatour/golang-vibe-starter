package item_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

func debugLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(httpx.NewContextHandler(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

func parseLogs(t *testing.T, buf *bytes.Buffer) []map[string]any {
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

// TestHTTPStoreCallLogging checks the log line for each kind of upstream
// outcome: its level, and its upstream_* fields.
func TestHTTPStoreCallLogging(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantLevel  string
		wantMsg    string
		wantStatus float64 // 0 means the field must be absent
	}{
		{
			name: "success is debug",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"id":"a","name":"x"}`))
			},
			wantLevel: "DEBUG", wantMsg: "upstream request", wantStatus: 200,
		},
		{
			name: "json 404 is an answer, not a failure",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":"item not found"}`))
			},
			wantLevel: "DEBUG", wantMsg: "upstream request", wantStatus: 404,
		},
		{
			name: "5xx is a warning",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			wantLevel: "WARN", wantMsg: "upstream request failed", wantStatus: 503,
		},
		{
			name: "bad body is a warning with the status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"id":`))
			},
			wantLevel: "WARN", wantMsg: "upstream request failed", wantStatus: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(tt.handler)
			defer upstream.Close()

			var logs bytes.Buffer
			store := item.NewHTTPStore(upstream.URL, upstream.Client(), item.WithStoreLogger(debugLogger(&logs)))
			_, _ = store.Get(t.Context(), "a")

			lines := parseLogs(t, &logs)
			if len(lines) != 1 {
				t.Fatalf("got %d log lines, want 1: %v", len(lines), lines)
			}
			l := lines[0]
			if l["level"] != tt.wantLevel || l["msg"] != tt.wantMsg {
				t.Errorf("log = %s %v, want %s %s", l["level"], l["msg"], tt.wantLevel, tt.wantMsg)
			}
			if l[item.LogKeyUpstream] != "GET /items/a" {
				t.Errorf("upstream = %v, want GET /items/a", l[item.LogKeyUpstream])
			}
			if got, _ := l[item.LogKeyUpstreamStatus].(float64); got != tt.wantStatus {
				t.Errorf("upstream_status = %v, want %v", l[item.LogKeyUpstreamStatus], tt.wantStatus)
			}
			if _, ok := l[item.LogKeyUpstreamDurationMS].(float64); !ok {
				t.Errorf("upstream_duration_ms = %v, want a number", l[item.LogKeyUpstreamDurationMS])
			}
			if tt.wantLevel == "WARN" && l[httpx.LogKeyError] == nil {
				t.Errorf("warning has no error field: %v", l)
			}
		})
	}
}

func TestHTTPStoreNoLoggerByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()
	// Must not panic or write anywhere without WithStoreLogger.
	_, _ = item.NewHTTPStore(upstream.URL, upstream.Client()).List(t.Context())
}

func TestUpstreamErrorFields(t *testing.T) {
	t.Run("bad status", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer upstream.Close()

		_, err := item.NewHTTPStore(upstream.URL, upstream.Client()).List(t.Context())
		var ue *item.UpstreamError
		if !errors.As(err, &ue) {
			t.Fatalf("error = %v (%T), want *UpstreamError", err, err)
		}
		if ue.Call != "GET /items" || ue.Status != http.StatusBadGateway || ue.Duration <= 0 {
			t.Errorf("UpstreamError = %+v, want GET /items, status 502, positive duration", ue)
		}
	})

	t.Run("no response has no status", func(t *testing.T) {
		upstream := httptest.NewServer(http.NotFoundHandler())
		url := upstream.URL
		upstream.Close()

		_, err := item.NewHTTPStore(url, &http.Client{Timeout: time.Second}).List(t.Context())
		var ue *item.UpstreamError
		if !errors.As(err, &ue) || ue.Status != 0 {
			t.Fatalf("error = %v, want *UpstreamError with status 0", err)
		}
		for _, a := range ue.LogAttrs() {
			if a.Key == item.LogKeyUpstreamStatus {
				t.Error("LogAttrs includes upstream_status without a response")
			}
		}
	})

	t.Run("not found stays a plain ErrNotFound", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"item not found"}`))
		}))
		defer upstream.Close()

		_, err := item.NewHTTPStore(upstream.URL, upstream.Client()).Get(t.Context(), "a")
		if err != item.ErrNotFound {
			t.Errorf("error = %#v, want exactly ErrNotFound", err)
		}
	})
}

// TestUpstreamTimeoutLogged checks a slow upstream: a warning with no status
// (no response arrived) and a duration near the client timeout.
func TestUpstreamTimeoutLogged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	store := item.NewHTTPStore(upstream.URL, &http.Client{Timeout: 50 * time.Millisecond}, item.WithStoreLogger(debugLogger(&logs)))
	if _, err := store.List(t.Context()); err == nil {
		t.Fatal("List() error = nil, want a timeout")
	}
	l := parseLogs(t, &logs)[0]
	if l["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", l["level"])
	}
	if _, ok := l[item.LogKeyUpstreamStatus]; ok {
		t.Errorf("timeout line has upstream_status: %v", l)
	}
	if d, _ := l[item.LogKeyUpstreamDurationMS].(float64); d < 40 {
		t.Errorf("upstream_duration_ms = %v, want about 50", d)
	}
}

// TestUpstreamCancelledIsDebug checks that a client abandoning the request
// isn't reported as an upstream failure.
func TestUpstreamCancelledIsDebug(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var logs bytes.Buffer
	store := item.NewHTTPStore(upstream.URL, upstream.Client(), item.WithStoreLogger(debugLogger(&logs)))
	_, err := store.List(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want one wrapping context.Canceled", err)
	}
	if l := parseLogs(t, &logs)[0]; l["level"] != "DEBUG" {
		t.Errorf("level = %v, want DEBUG: %v", l["level"], l)
	}
}

// TestUpstreamFailureEndToEnd sends a request through the middleware and
// handlers to a failing upstream, and checks that the lines logged for it
// share a request_id and that the "internal error" line has the upstream
// fields: one line is enough to see what the upstream did.
func TestUpstreamFailureEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"db down"}`))
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	logger := debugLogger(&logs)
	store := item.NewHTTPStore(upstream.URL, upstream.Client(), item.WithStoreLogger(logger))
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, logger, store)

	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	req.Header.Set(httpx.RequestIDHeader, "corr-9")
	rec := httptest.NewRecorder()
	httpx.Middleware(logger, mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	byMsg := map[string]map[string]any{}
	for _, l := range parseLogs(t, &logs) {
		byMsg[l["msg"].(string)] = l
		if l[httpx.LogKeyRequestID] != "corr-9" {
			t.Errorf("%q line request_id = %v, want corr-9", l["msg"], l[httpx.LogKeyRequestID])
		}
	}
	for _, msg := range []string{"upstream request failed", "internal error", "request"} {
		if byMsg[msg] == nil {
			t.Fatalf("no %q line in logs: %s", msg, logs.String())
		}
	}
	ie := byMsg["internal error"]
	if ie["level"] != "ERROR" || ie[item.LogKeyUpstream] != "GET /items" || ie[item.LogKeyUpstreamStatus] != float64(503) {
		t.Errorf("internal error line = %v, want ERROR with upstream=GET /items, upstream_status=503", ie)
	}
	if _, ok := ie[item.LogKeyUpstreamDurationMS].(float64); !ok {
		t.Errorf("internal error line has no upstream_duration_ms: %v", ie)
	}
	if rec.Body.String() == "" || bytes.Contains(rec.Body.Bytes(), []byte("db down")) {
		t.Errorf("response body %q leaks the upstream message", rec.Body)
	}
}

// TestUpstreamErrorsAreRedactedAndBounded checks what can reach a log line
// through an upstream error: credentials in the base URL are masked, the
// upstream's own error message is capped, and so is a very long path.
func TestUpstreamErrorsAreRedactedAndBounded(t *testing.T) {
	t.Run("credentials in the base URL", func(t *testing.T) {
		dead := httptest.NewServer(http.NotFoundHandler())
		base := dead.URL
		dead.Close()
		base = "http://alice:s3cret@" + base[len("http://"):]

		_, err := item.NewHTTPStore(base, &http.Client{Timeout: time.Second}).List(t.Context())
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "***") {
			t.Errorf("error %q shows the password", err)
		}
	})

	t.Run("upstream error message is capped", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": strings.Repeat("x", 5000)})
		}))
		defer upstream.Close()

		_, err := item.NewHTTPStore(upstream.URL, upstream.Client()).List(t.Context())
		if err == nil || len(err.Error()) > 400 {
			t.Errorf("error is %d bytes, want it capped well under 400: %.120q", len(err.Error()), err)
		}
	})

	t.Run("long path is capped in the error and the log", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer upstream.Close()

		var logs bytes.Buffer
		store := item.NewHTTPStore(upstream.URL, upstream.Client(), item.WithStoreLogger(debugLogger(&logs)))
		_, err := store.Get(t.Context(), strings.Repeat("a", 5000))
		if err == nil || len(err.Error()) > 1000 {
			t.Errorf("error is %d bytes, want it capped", len(err.Error()))
		}
		if logs.Len() > 2000 {
			t.Errorf("log output is %d bytes, want it capped", logs.Len())
		}
	})
}
