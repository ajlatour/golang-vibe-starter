package item_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
)

// debugLogger returns a debug-level logger on buf that, like the service's
// own, adds request_id to lines logged with a request's context.
func debugLogger(buf *logtest.Buffer) *slog.Logger {
	return slog.New(httpx.NewContextHandler(buf.Handler(slog.LevelDebug)))
}

// logLine is the level and message of one expected log line.
type logLine struct{ level, msg string }

const (
	sent     = "upstream request sent"
	received = "upstream response received"
	failed   = "upstream request failed"
)

// TestHTTPStoreCallLogging checks the lines an upstream call writes for each
// kind of outcome: a "sent" line before the call, a "received" line whenever
// a response came back, and a "failed" line if the call didn't succeed.
func TestHTTPStoreCallLogging(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		want       []logLine
		wantStatus float64
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"id":"a","name":"x"}`))
			},
			want:       []logLine{{"INFO", sent}, {"INFO", received}},
			wantStatus: 200,
		},
		{
			name: "json 404 is an answer, not a failure",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":"item not found"}`))
			},
			want:       []logLine{{"INFO", sent}, {"INFO", received}},
			wantStatus: 404,
		},
		{
			name:       "5xx: a response arrived, and the call failed",
			handler:    nettest.Status(http.StatusServiceUnavailable, ""),
			want:       []logLine{{"INFO", sent}, {"INFO", received}, {"WARN", failed}},
			wantStatus: 503,
		},
		{
			name:       "bad body: a response arrived, and the call failed",
			handler:    nettest.Status(http.StatusOK, `{"id":`),
			want:       []logLine{{"INFO", sent}, {"INFO", received}, {"WARN", failed}},
			wantStatus: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(tt.handler)
			defer upstream.Close()

			var logs logtest.Buffer
			store := item.NewHTTPStore(upstream.URL, upstream.Client(), item.WithStoreLogger(debugLogger(&logs)))
			_, _ = store.Get(t.Context(), "a")

			lines := logs.Records(t)
			var got []logLine
			for _, l := range lines {
				got = append(got, logLine{l["level"].(string), l["msg"].(string)})
				if l[httpx.LogKeyUpstream] != "GET /items/a" {
					t.Errorf("%v: upstream = %v, want GET /items/a", l["msg"], l[httpx.LogKeyUpstream])
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("lines = %v, want %v", got, tt.want)
			}

			// The "sent" line is logged before the call, so it has no outcome.
			for _, key := range []string{httpx.LogKeyUpstreamStatus, httpx.LogKeyUpstreamDurationMS} {
				if _, ok := lines[0][key]; ok {
					t.Errorf("sent line has %s: %v", key, lines[0])
				}
			}
			for _, l := range lines[1:] {
				if got, _ := l[httpx.LogKeyUpstreamStatus].(float64); got != tt.wantStatus {
					t.Errorf("%v: upstream_status = %v, want %v", l["msg"], l[httpx.LogKeyUpstreamStatus], tt.wantStatus)
				}
				if _, ok := l[httpx.LogKeyUpstreamDurationMS].(float64); !ok {
					t.Errorf("%v: upstream_duration_ms = %v, want a number", l["msg"], l[httpx.LogKeyUpstreamDurationMS])
				}
			}
			if last := lines[len(lines)-1]; last["level"] == "WARN" && last[httpx.LogKeyError] == nil {
				t.Errorf("warning has no error field: %v", last)
			}
		})
	}
}

func TestHTTPStoreNoLoggerByDefault(t *testing.T) {
	upstream := httptest.NewServer(nettest.Status(http.StatusBadGateway, ""))
	defer upstream.Close()
	// Must not panic or write anywhere without WithStoreLogger.
	_, _ = item.NewHTTPStore(upstream.URL, upstream.Client()).List(t.Context())
}

func TestUpstreamErrorFields(t *testing.T) {
	t.Run("bad status", func(t *testing.T) {
		upstream := httptest.NewServer(nettest.Status(http.StatusBadGateway, ""))
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
		_, err := item.NewHTTPStore(nettest.DeadURL(), &http.Client{Timeout: time.Second}).List(t.Context())
		var ue *item.UpstreamError
		if !errors.As(err, &ue) || ue.Status != 0 {
			t.Fatalf("error = %v, want *UpstreamError with status 0", err)
		}
		for _, a := range ue.LogAttrs() {
			if a.Key == httpx.LogKeyUpstreamStatus {
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
	upstream := httptest.NewServer(http.HandlerFunc(nettest.Hang))
	defer upstream.Close()

	var logs logtest.Buffer
	store := item.NewHTTPStore(upstream.URL, &http.Client{Timeout: 50 * time.Millisecond}, item.WithStoreLogger(debugLogger(&logs)))
	if _, err := store.List(t.Context()); err == nil {
		t.Fatal("List() error = nil, want a timeout")
	}
	lines := logs.Records(t)
	if len(lines) != 2 || lines[0]["msg"] != sent || lines[1]["msg"] != failed {
		t.Fatalf("lines = %v, want sent then failed (no response arrived, so no received line)", lines)
	}
	l := lines[1]
	if l["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", l["level"])
	}
	if _, ok := l[httpx.LogKeyUpstreamStatus]; ok {
		t.Errorf("timeout line has upstream_status: %v", l)
	}
	if d, _ := l[httpx.LogKeyUpstreamDurationMS].(float64); d < 40 {
		t.Errorf("upstream_duration_ms = %v, want about 50", d)
	}
}

// TestUpstreamCancelledIsDebug checks that a client abandoning the request
// isn't reported as an upstream failure.
func TestUpstreamCancelledIsDebug(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(nettest.Hang))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var logs logtest.Buffer
	store := item.NewHTTPStore(upstream.URL, upstream.Client(), item.WithStoreLogger(debugLogger(&logs)))
	_, err := store.List(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want one wrapping context.Canceled", err)
	}
	lines := logs.Records(t)
	if last := lines[len(lines)-1]; last["msg"] != "upstream request cancelled" || last["level"] != "DEBUG" {
		t.Errorf("last line = %v, want DEBUG upstream request cancelled", last)
	}
}

// TestUpstreamFailureEndToEnd sends a request through the middleware and
// handlers to a failing upstream, and checks that the lines logged for it
// share a request_id and that the "internal error" line has the upstream
// fields: one line is enough to see what the upstream did.
func TestUpstreamFailureEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(nettest.Status(http.StatusServiceUnavailable, `{"error":"db down"}`))
	defer upstream.Close()

	var logs logtest.Buffer
	logger := debugLogger(&logs)
	store := item.NewHTTPStore(upstream.URL, upstream.Client(), item.WithStoreLogger(logger))
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, logger, store)

	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	req.Header.Set(httpx.RequestIDHeader, "corr-9")
	rec := httptest.NewRecorder()
	httpx.Middleware(logger, mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for an upstream 503", rec.Code)
	}
	byMsg := map[string]map[string]any{}
	for _, l := range logs.Records(t) {
		byMsg[l["msg"].(string)] = l
		if l[httpx.LogKeyRequestID] != "corr-9" {
			t.Errorf("%q line request_id = %v, want corr-9", l["msg"], l[httpx.LogKeyRequestID])
		}
	}
	for _, msg := range []string{
		"request received", sent, received, failed, "internal error", "response sent",
	} {
		if byMsg[msg] == nil {
			t.Fatalf("no %q line in logs: %s", msg, logs.String())
		}
	}
	ie := byMsg["internal error"]
	if ie["level"] != "ERROR" || ie[httpx.LogKeyUpstream] != "GET /items" || ie[httpx.LogKeyUpstreamStatus] != float64(503) {
		t.Errorf("internal error line = %v, want ERROR with upstream=GET /items, upstream_status=503", ie)
	}
	if _, ok := ie[httpx.LogKeyUpstreamDurationMS].(float64); !ok {
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
		base := "http://alice:s3cret@" + strings.TrimPrefix(nettest.DeadURL(), "http://")

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
		upstream := httptest.NewServer(nettest.Status(http.StatusInternalServerError, ""))
		defer upstream.Close()

		var logs logtest.Buffer
		store := item.NewHTTPStore(upstream.URL, upstream.Client(), item.WithStoreLogger(debugLogger(&logs)))
		_, err := store.Get(t.Context(), strings.Repeat("a", 5000))
		if err == nil || len(err.Error()) > 1000 {
			t.Errorf("error is %d bytes, want it capped", len(err.Error()))
		}
		if logs.Len() > 4000 {
			t.Errorf("log output is %d bytes, want it capped", logs.Len())
		}
	})
}

// TestUpstreamFailureStatuses checks what the client is told when the
// upstream can't give a usable answer: 504 for a timeout, 502 for everything
// else, and never a 500, which would say the API itself broke. The body is
// the generic reason phrase, with no upstream detail.
func TestUpstreamFailureStatuses(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc // nil: nothing is listening
		timeout    time.Duration
		wantStatus int
	}{
		{
			name:       "timeout",
			handler:    nettest.Hang,
			timeout:    50 * time.Millisecond,
			wantStatus: http.StatusGatewayTimeout,
		},
		{name: "connection refused", handler: nil, wantStatus: http.StatusBadGateway},
		{
			name:       "connection dropped mid-request",
			handler:    nettest.Drop,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "upstream 503",
			handler:    nettest.Status(http.StatusServiceUnavailable, `{"error":"secret upstream detail"}`),
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "upstream 500",
			handler:    nettest.Status(http.StatusInternalServerError, ""),
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "undecodable body",
			handler:    nettest.Status(http.StatusOK, `{"items":`),
			wantStatus: http.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var base string
			client := &http.Client{Timeout: 5 * time.Second}
			if tt.timeout != 0 {
				client.Timeout = tt.timeout
			}
			if tt.handler == nil {
				base = nettest.DeadURL()
			} else {
				upstream := httptest.NewServer(tt.handler)
				defer upstream.Close()
				base = upstream.URL
			}

			mux := http.NewServeMux()
			item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), item.NewHTTPStore(base, client))
			for _, path := range []string{"/items", "/items/a"} {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

				if rec.Code != tt.wantStatus {
					t.Errorf("GET %s status = %d, want %d", path, rec.Code, tt.wantStatus)
				}
				var body struct{ Error string }
				decodeBody(t, rec, &body)
				if want := strings.ToLower(http.StatusText(tt.wantStatus)); body.Error != want {
					t.Errorf("GET %s error = %q, want the generic %q", path, body.Error, want)
				}
				if strings.Contains(rec.Body.String(), "secret") {
					t.Errorf("response leaks upstream detail: %s", rec.Body)
				}
			}
		})
	}
}

// TestPlainStoreErrorStaysA500 checks that only upstream failures are 5xx
// other than 500: an unexpected error from anywhere else still is one.
func TestPlainStoreErrorStaysA500(t *testing.T) {
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), failingStore{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
