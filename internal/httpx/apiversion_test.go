package httpx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIVersion(t *testing.T) {
	h := Middleware(slog.New(slog.DiscardHandler), APIVersion([]string{"/healthz"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	for _, tt := range []struct {
		path, version string
		want          int
	}{
		{"/items", "", http.StatusNoContent},  // none: the current version
		{"/items", "1", http.StatusNoContent}, // a version it serves
		{"/items", "2", http.StatusBadRequest},
		{"/items", "v1", http.StatusBadRequest},
		{"/healthz", "2", http.StatusNoContent}, // the probes answer whatever is asked
	} {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		if tt.version != "" {
			req.Header.Set(APIVersionHeader, tt.version)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("%s with API-Version %q: %d, want %d", tt.path, tt.version, rec.Code, tt.want)
		}
		if got := rec.Header().Get(APIVersionHeader); got != "1" {
			t.Errorf("%s with API-Version %q: response API-Version %q, want 1 on every response", tt.path, tt.version, got)
		}
		if tt.want == http.StatusBadRequest && !strings.Contains(rec.Body.String(), `"code":"unsupported_api_version"`) {
			t.Errorf("refusal body %s: want code unsupported_api_version", rec.Body)
		}
	}
}
