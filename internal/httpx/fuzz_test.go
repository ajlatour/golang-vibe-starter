package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FuzzDecodeJSON throws arbitrary bodies at the request decoder: it must never
// panic, and an error, which goes to the client, must never describe the
// service's Go types.
func FuzzDecodeJSON(f *testing.F) {
	for _, seed := range []string{
		`{"name":"x"}`, `{"name":1}`, `["x"]`, `{"name":"x","extra":1}`, `{"name":"x"}{}`,
		`{"name":`, ``, `null`, `{"name":"\u0000"}`, `{"name":{"nested":[1,2,3]}}`, "\xff\xfe",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		var v struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		r := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(string(body)))
		err := DecodeJSON(httptest.NewRecorder(), r, &v)
		if err != nil && (strings.Contains(err.Error(), "Go ") || strings.Contains(err.Error(), "struct")) {
			t.Errorf("error %q names Go internals", err)
		}
	})
}

// FuzzCorrelationID sends arbitrary correlation IDs: the service echoes one
// only if it's safe to put in a log line, and otherwise makes up its own.
func FuzzCorrelationID(f *testing.F) {
	for _, seed := range []string{"abc", "svc-a:req_42.1", "", "has space", `x"y`, "a\nb", "é", strings.Repeat("a", 129)} {
		f.Add(seed)
	}
	h := assignRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	f.Fuzz(func(t *testing.T, id string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header[RequestIDHeader] = []string{id}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		got := rec.Header().Get(RequestIDHeader)
		if !validRequestID(got) {
			t.Fatalf("echoed %q, which isn't a valid ID", got)
		}
		if got != id && validRequestID(id) {
			t.Errorf("a valid ID %q was replaced with %q", id, got)
		}
		for _, c := range []byte(got) {
			if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
				t.Fatalf("echoed ID %q contains %q, which doesn't belong in a log line", got, c)
			}
		}
	})
}
