package limit_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/limit"
)

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }

func get(h http.Handler, path, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPerClient(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	h := limit.PerClientAt(limit.Policy{Rate: 2, Burst: 3, Exempt: []string{"/healthz"}}, clock, http.HandlerFunc(ok))

	// A burst of 3, then refused.
	for i := range 3 {
		if rec := get(h, "/items", "10.0.0.1:1234"); rec.Code != 204 {
			t.Fatalf("request %d of the burst: %d, want 204", i+1, rec.Code)
		}
	}
	rec := get(h, "/items", "10.0.0.1:1234")
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("over the burst: %d, Retry-After %q; want 429 and 1", rec.Code, rec.Header().Get("Retry-After"))
	}
	// Another client, and the healthcheck, are unaffected.
	if rec := get(h, "/items", "10.0.0.2:1234"); rec.Code != 204 {
		t.Errorf("another client: %d, want 204", rec.Code)
	}
	if rec := get(h, "/healthz", "10.0.0.1:1234"); rec.Code != 204 {
		t.Errorf("the healthcheck: %d, want 204", rec.Code)
	}
	// At 2 per second, half a second brings one token back.
	now = now.Add(500 * time.Millisecond)
	if rec := get(h, "/items", "10.0.0.1:9999"); rec.Code != 204 {
		t.Errorf("after the refill, from another port of the same IP: %d, want 204", rec.Code)
	}
	if rec := get(h, "/items", "10.0.0.1:1234"); rec.Code != 429 {
		t.Errorf("the refilled token spent: %d, want 429", rec.Code)
	}
	// A long quiet spell refills to the burst, no further.
	now = now.Add(time.Hour)
	for i := range 3 {
		if rec := get(h, "/items", "10.0.0.1:1234"); rec.Code != 204 {
			t.Fatalf("request %d after an hour: %d, want 204", i+1, rec.Code)
		}
	}
	if rec := get(h, "/items", "10.0.0.1:1234"); rec.Code != 429 {
		t.Errorf("the burst after an hour: %d, want 429", rec.Code)
	}
}

func TestRetryAfterRoundsUp(t *testing.T) {
	now := time.Unix(0, 0)
	h := limit.PerClientAt(limit.Policy{Rate: 0.2, Burst: 1}, func() time.Time { return now }, http.HandlerFunc(ok))
	get(h, "/items", "10.0.0.1:1")
	now = now.Add(time.Second)
	// 1 of 5 seconds has passed: 4 to go.
	if got := get(h, "/items", "10.0.0.1:1").Header().Get("Retry-After"); got != "4" {
		t.Errorf("Retry-After = %q, want 4", got)
	}
}

func TestNoLimits(t *testing.T) {
	h := limit.InFlight(limit.Policy{}, limit.PerClient(limit.Policy{}, http.HandlerFunc(ok)))
	for range 1000 {
		if rec := get(h, "/items", "10.0.0.1:1"); rec.Code != 204 {
			t.Fatalf("with no limits: %d", rec.Code)
		}
	}
}

func TestInFlight(t *testing.T) {
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(2)
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			started.Done()
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := limit.InFlight(limit.Policy{MaxInFlight: 2, Exempt: []string{"/healthz"}}, slow)

	var done sync.WaitGroup
	for range 2 {
		done.Add(1)
		go func() { defer done.Done(); get(h, "/slow", "10.0.0.1:1") }()
	}
	started.Wait()

	rec := get(h, "/items", "10.0.0.9:1")
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "1" {
		t.Errorf("with 2 of 2 in flight: %d, Retry-After %q; want 503 and 1", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := get(h, "/healthz", "10.0.0.9:1"); rec.Code != 204 {
		t.Errorf("the healthcheck while full: %d, want 204", rec.Code)
	}
	close(release)
	done.Wait()
	if rec := get(h, "/items", "10.0.0.9:1"); rec.Code != 204 {
		t.Errorf("after the slow requests finished: %d, want 204", rec.Code)
	}
}

func TestValidate(t *testing.T) {
	for name, p := range map[string]limit.Policy{
		"negative cap":       {MaxInFlight: -1},
		"negative rate":      {Rate: -1},
		"rate with no burst": {Rate: 10},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want an error", name)
		}
	}
	if err := (limit.Policy{MaxInFlight: 512, Rate: 100, Burst: 200}).Validate(); err != nil {
		t.Errorf("the defaults: %v", err)
	}
}
