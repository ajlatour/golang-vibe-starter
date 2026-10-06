package httpx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantStatus int
	}{
		{
			name:       "ok",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) },
			wantStatus: http.StatusOK,
		},
		{
			name:       "explicit status",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) },
			wantStatus: http.StatusTeapot,
		},
		{
			name:       "panic becomes 500",
			handler:    func(http.ResponseWriter, *http.Request) { panic("boom") },
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs logtest.Buffer
			h := Middleware(slog.New(logx.NewContextHandler(logs.Handler(slog.LevelInfo))), tt.handler)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			// The last log line is "response sent"; it should record the
			// status the client actually got.
			recs := logs.Records(t)
			last := recs[len(recs)-1]
			if last["msg"] != "response sent" || last["status"] != float64(tt.wantStatus) {
				t.Errorf("last log line = %v, want msg=response sent status=%d", last, tt.wantStatus)
			}
		})
	}
}

// TestDefensiveHeadersAreNotShared checks a handler that adds to a defensive
// header changes only its own response, though every response starts from
// the same values.
func TestDefensiveHeadersAreNotShared(t *testing.T) {
	h := Middleware(slog.New(slog.DiscardHandler), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/private" {
			w.Header().Add("Cache-Control", "private")
		}
	}))
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest("GET", "/private", nil))
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest("GET", "/other", nil))
	if got := first.Header()["Cache-Control"]; len(got) != 2 {
		t.Errorf("the first response's Cache-Control = %q, want no-store and private", got)
	}
	if got := second.Header()["Cache-Control"]; len(got) != 1 || got[0] != "no-store" {
		t.Errorf("the next response's Cache-Control = %q: the first one's Add leaked into it", got)
	}
}

// countingMetrics counts the requests started and finished.
type countingMetrics struct{ started, observed int }

func (m *countingMetrics) Start() { m.started++ }
func (m *countingMetrics) Observe(route, method string, status int, d time.Duration) {
	m.observed++
}

// TestMiddlewareMetricsSurviveAbort checks a request whose handler aborts
// with http.ErrAbortHandler, which the middleware passes on to net/http, is
// still observed as finished, so the in-flight count doesn't drift.
func TestMiddlewareMetricsSurviveAbort(t *testing.T) {
	m := &countingMetrics{}
	h := Middleware(slog.New(slog.DiscardHandler), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}), WithMetrics(m))
	func() {
		defer func() {
			if v := recover(); v != http.ErrAbortHandler {
				t.Errorf("recovered %v, want http.ErrAbortHandler passed on", v)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items", nil))
	}()
	if m.started != 1 || m.observed != 1 {
		t.Errorf("started %d, observed %d; want 1 and 1", m.started, m.observed)
	}
}

// TestResponseSentSaysWhy checks "response sent" carries the error message
// WriteError sent as response_error, bounded, through a wrapped writer too,
// and nothing when the response wasn't an error.
func TestResponseSentSaysWhy(t *testing.T) {
	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
		want    any // response_error, or nil for none
	}{
		{"an error", func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, http.StatusUnprocessableEntity, "name is required")
		}, "name is required"},
		{"through a wrapped writer", func(w http.ResponseWriter, r *http.Request) {
			WriteError(wrapped{w}, http.StatusTooManyRequests, "too many requests; slow down")
		}, "too many requests; slow down"},
		{"a long one, quoting the client", func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, http.StatusBadRequest, `invalid JSON: unknown field "`+strings.Repeat("x", 500)+`"`)
		}, `invalid JSON: unknown field "` + strings.Repeat("x", maxLoggedResponseError-len(`invalid JSON: unknown field "`)) + "…"},
		{"no error", func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs logtest.Buffer
			h := Middleware(logs.Logger(slog.LevelInfo), tt.handler)
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/items", nil))
			var sent map[string]any
			for _, r := range logs.Records(t) {
				if r["msg"] == "response sent" {
					sent = r
				}
			}
			if sent == nil {
				t.Fatalf("no response sent line:\n%s", logs.String())
			}
			if got := sent[logx.KeyResponseError]; got != tt.want {
				t.Errorf("response_error = %q, want %q", got, tt.want)
			}
		})
	}
}

// wrapped is a ResponseWriter wrapper that can be unwrapped, as
// http.ResponseController expects of one.
type wrapped struct{ http.ResponseWriter }

func (w wrapped) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// TestClientIP checks WithClientIP puts the connection's address, without
// its port, on both of a request's lines, and that without it neither line
// carries one.
func TestClientIP(t *testing.T) {
	for _, tt := range []struct {
		remoteAddr string
		opts       []MiddlewareOption
		want       any // client_ip, or nil for none
	}{
		{"192.0.2.7:51234", []MiddlewareOption{WithClientIP()}, "192.0.2.7"},
		{"[2001:db8::7]:51234", []MiddlewareOption{WithClientIP()}, "2001:db8::7"},
		{"192.0.2.7", []MiddlewareOption{WithClientIP()}, "192.0.2.7"}, // no port: as it is
		{"192.0.2.7:51234", nil, nil},
	} {
		var logs logtest.Buffer
		h := Middleware(logs.Logger(slog.LevelInfo), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), tt.opts...)
		req := httptest.NewRequest(http.MethodGet, "/items", nil)
		req.RemoteAddr = tt.remoteAddr
		h.ServeHTTP(httptest.NewRecorder(), req)
		recs := logs.Records(t)
		if len(recs) != 2 {
			t.Fatalf("%d lines, want request received and response sent", len(recs))
		}
		for _, r := range recs {
			if got := r[logx.KeyClientIP]; got != tt.want {
				t.Errorf("%s, options %d: %s has client_ip %v, want %v", tt.remoteAddr, len(tt.opts), r["msg"], got, tt.want)
			}
		}
	}
}
