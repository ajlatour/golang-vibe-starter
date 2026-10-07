package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

func TestChainIsOutermostFirst(t *testing.T) {
	var met []string
	step := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				met = append(met, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { met = append(met, "handler") }),
		step("a"), step("b"), step("c"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got := strings.Join(met, ","); got != "a,b,c,handler" {
		t.Errorf("met %s, want a,b,c,handler", got)
	}
	met = nil
	httpx.Chain(h).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got := strings.Join(met, ","); got != "a,b,c,handler" {
		t.Errorf("an empty chain added steps: met %s", got)
	}
}
