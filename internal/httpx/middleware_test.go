package httpx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
