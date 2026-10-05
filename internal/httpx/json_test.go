package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	tests := []struct {
		name     string
		body     string
		wantName string
		wantErr  string // substring; "" means success
	}{
		{name: "valid", body: `{"name":"x"}`, wantName: "x"},
		{name: "valid with trailing whitespace", body: "{\"name\":\"x\"}\n", wantName: "x"},
		{name: "empty", body: ``, wantErr: "empty"},
		{name: "malformed", body: `{"name":`, wantErr: "invalid JSON"},
		{name: "wrong type", body: `{"name":1}`, wantErr: `invalid JSON: field "name" must be a string`},
		{name: "not an object", body: `["x"]`, wantErr: "invalid JSON: the body must be an object"},
		{name: "unknown field", body: `{"name":"x","extra":1}`, wantErr: `invalid JSON: unknown field "extra"`},
		{name: "trailing data", body: `{"name":"x"}{}`, wantErr: "single JSON object"},
		{name: "too large", body: `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, wantErr: "exceeds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			var got payload
			err := DecodeJSON(httptest.NewRecorder(), req, &got)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("DecodeJSON() error = %v", err)
				}
				if got.Name != tt.wantName {
					t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("DecodeJSON() error = %v, want it to contain %q", err, tt.wantErr)
			}
			// The message goes to the client, so it mustn't describe the
			// server's Go types.
			if err != nil && (strings.Contains(err.Error(), "Go ") || strings.Contains(err.Error(), "struct")) {
				t.Errorf("DecodeJSON() error = %q names Go internals", err)
			}
		})
	}
}
