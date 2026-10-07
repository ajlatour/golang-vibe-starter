package httpx_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

func routedMux() *http.ServeMux {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }
	mux.HandleFunc("GET /items", ok)
	mux.HandleFunc("POST /items", ok)
	mux.HandleFunc("GET /items/{id}", ok)
	mux.HandleFunc("DELETE /items/{id}", ok)
	httpx.HandleUnmatched(mux)
	return mux
}

func TestHandleUnmatched(t *testing.T) {
	var logs logtest.Buffer
	h := httpx.Middleware(slog.New(logx.NewContextHandler(logs.Handler(slog.LevelInfo))), httpx.RejectInvalidTarget(httpx.RecordRoute(routedMux())))
	for _, tc := range []struct {
		method, target string
		status         int
		code, allow    string
	}{
		{"GET", "/items", http.StatusNoContent, "", ""},
		{"HEAD", "/items/x", http.StatusNoContent, "", ""}, // HEAD rides on GET, as ServeMux has it
		{"GET", "/nope", http.StatusNotFound, "not_found", ""},
		{"GET", "/items/x/y", http.StatusNotFound, "not_found", ""},
		{"GET", "/", http.StatusNotFound, "not_found", ""},
		{"PATCH", "/items", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD, POST"},
		{"PUT", "/items/x", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD, DELETE"},
		{"OPTIONS", "*", http.StatusBadRequest, "invalid_target", ""},
	} {
		logs.Reset()
		r := httptest.NewRequest(tc.method, "/", nil)
		r.RequestURI = tc.target
		r.URL.Path = tc.target
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		name := tc.method + " " + tc.target
		if w.Code != tc.status {
			t.Errorf("%s: status %d, want %d", name, w.Code, tc.status)
			continue
		}
		if got := w.Header().Get("Allow"); got != tc.allow {
			t.Errorf("%s: Allow %q, want %q", name, got, tc.allow)
		}
		if tc.code == "" {
			continue
		}
		var body struct{ Error, Code string }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != tc.code || body.Error == "" {
			t.Errorf("%s: body %q (Content-Type %q), want JSON with code %s", name, w.Body, w.Header().Get("Content-Type"), tc.code)
		}
		// Logged with what was sent, and no route: it matched none.
		var sent map[string]any
		for _, rec := range logs.Records(t) {
			if rec["msg"] == "response sent" {
				sent = rec
			}
		}
		if sent == nil || sent["status"] != float64(tc.status) || sent["route"] != "" || sent["response_code"] != tc.code {
			t.Errorf("%s: response sent = %v", name, sent)
		}
	}
}
