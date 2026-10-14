package main

import (
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// TestOpenAPICorrelationIDMatchesBehavior checks the Client-Correlation-Id
// header schema against what the service accepts: an ID that fits the spec's
// pattern is echoed back, and one that doesn't is replaced by a generated one
// that does.
func TestOpenAPICorrelationIDMatchesBehavior(t *testing.T) {
	s := loadSpec(t)
	param, ok := s.lookup("#/components/parameters/CorrelationID")
	if !ok {
		t.Fatal("spec has no CorrelationID parameter")
	}
	if param["name"] != httpx.RequestIDHeader {
		t.Errorf("spec names the header %q; the code uses %q", param["name"], httpx.RequestIDHeader)
	}
	pattern := regexp.MustCompile(param["schema"].(map[string]any)["pattern"].(string))

	handler, _ := handlerFor(t, "ok")
	for _, id := range []string{
		"abc", "3f2504e0-4f89-11d3-9a0c-0305e82c3301", "svc-a:req_42.1",
		strings.Repeat("a", 128), strings.Repeat("a", 129),
		"has space", `x"y`, "a\nb", "a/b", "é", "",
	} {
		t.Run(truncate(strconv.Quote(id)), func(t *testing.T) {
			req := httptest.NewRequest("GET", "/healthz", nil)
			req.Header.Set(httpx.RequestIDHeader, id)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			got := rec.Header().Get(httpx.RequestIDHeader)
			if pattern.MatchString(id) {
				if got != id {
					t.Errorf("the spec accepts %q, but the service answered with %q instead of echoing it", id, got)
				}
				return
			}
			if got == id || !pattern.MatchString(got) {
				t.Errorf("the spec rejects %q, so the service should replace it with a valid generated ID; got %q", id, got)
			}
		})
	}
}
