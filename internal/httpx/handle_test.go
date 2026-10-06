package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

// gatewayErr asks ServerError for a 502, like a failed upstream call.
type gatewayErr struct{}

func (gatewayErr) Error() string   { return "upstream GET /items: connection refused" }
func (gatewayErr) HTTPStatus() int { return http.StatusBadGateway }

// TestHandle checks the one place a handler's error becomes its response:
// nothing for nil, an Error's own answer (wrapped or not, and not logged as
// a failure), and ServerError's for anything else.
func TestHandle(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		cancelled  bool
		wantStatus int
		wantCode   Code
		wantLogged bool // an "internal error" line
	}{
		{name: "nil", err: nil, wantStatus: http.StatusNoContent},
		{name: "an Error", err: NewError(http.StatusNotFound, CodeNotFound, "item not found"), wantStatus: 404, wantCode: CodeNotFound},
		{name: "a wrapped Error", err: fmt.Errorf("get: %w", &Error{Status: 422, Code: CodeInvalidField, Message: "name is required", Err: errors.New("cause")}), wantStatus: 422, wantCode: CodeInvalidField},
		{name: "a plain error", err: errors.New("boom"), wantStatus: 500, wantCode: CodeInternal, wantLogged: true},
		{name: "a StatusError", err: fmt.Errorf("list: %w", gatewayErr{}), wantStatus: 502, wantCode: CodeUpstreamFailed, wantLogged: true},
		{name: "the client left", err: context.Canceled, cancelled: true, wantStatus: StatusClientClosedRequest, wantCode: CodeClientClosed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs logtest.Buffer
			h := Handle(logs.Logger(slog.LevelInfo), func(w http.ResponseWriter, r *http.Request) error {
				if tt.err == nil {
					w.WriteHeader(http.StatusNoContent)
				}
				return tt.err
			})
			req := httptest.NewRequest(http.MethodGet, "/items", nil)
			if tt.cancelled {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCode != "" {
				var body struct {
					Error string `json:"error"`
					Code  Code   `json:"code"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != tt.wantCode {
					t.Errorf("body %s: want code %s", rec.Body, tt.wantCode)
				}
				if tt.wantStatus >= 500 && body.Error != "internal server error" && body.Error != "bad gateway" {
					t.Errorf("a 5xx's message %q gives away more than the status", body.Error)
				}
			}
			logged := false
			for _, r := range logs.Records(t) {
				logged = logged || r["msg"] == "internal error"
			}
			if logged != tt.wantLogged {
				t.Errorf("logged as an internal error: %v, want %v\n%s", logged, tt.wantLogged, logs.String())
			}
		})
	}
}
