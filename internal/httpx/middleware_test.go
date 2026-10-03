package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
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
			var logs bytes.Buffer
			h := Middleware(slog.New(slog.NewJSONHandler(&logs, nil)), tt.handler)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			// The last log line is the request log; it should record the
			// status the client actually got.
			lines := bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n"))
			var entry struct {
				Msg    string `json:"msg"`
				Status int    `json:"status"`
			}
			if err := json.Unmarshal(lines[len(lines)-1], &entry); err != nil {
				t.Fatalf("parse log line: %v", err)
			}
			if entry.Msg != "request" || entry.Status != tt.wantStatus {
				t.Errorf("request log = %+v, want msg=request status=%d", entry, tt.wantStatus)
			}
		})
	}
}
