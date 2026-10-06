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
	h := limit.PerClientAt(limit.Policy{Rate: 2, Burst: 3, Exempt: []string{"/healthz"}}, nil, clock, http.HandlerFunc(ok))

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
	h := limit.PerClientAt(limit.Policy{Rate: 0.2, Burst: 1}, nil, func() time.Time { return now }, http.HandlerFunc(ok))
	get(h, "/items", "10.0.0.1:1")
	now = now.Add(time.Second)
	// 1 of 5 seconds has passed: 4 to go.
	if got := get(h, "/items", "10.0.0.1:1").Header().Get("Retry-After"); got != "4" {
		t.Errorf("Retry-After = %q, want 4", got)
	}
}

func TestNoLimits(t *testing.T) {
	h := limit.InFlight(limit.Policy{}, limit.PerClient(limit.Policy{}, nil, http.HandlerFunc(ok)))
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

// identifyByHeader identifies a request by its X-Client header, as that
// client with the given limit (a zero rate: the policy's), standing in for
// authentication, which cmd/api plugs in the same way.
func identifyByHeader(rate float64, burst int) limit.Identify {
	return func(r *http.Request) (limit.Identity, bool) {
		c := r.Header.Get("X-Client")
		if c == "" {
			return limit.Identity{}, false
		}
		return limit.Identity{Client: c, Rate: rate, Burst: burst}, true
	}
}

// getAs sends a request as client, from addr; an empty client is
// unidentified.
func getAs(h http.Handler, client, addr string) int {
	req := httptest.NewRequest("GET", "/items", nil)
	req.RemoteAddr = addr
	if client != "" {
		req.Header.Set("X-Client", client)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestPerClientIdentityLimits checks an identified client's own limit
// replaces the policy's, and applies even with no policy limit at all.
func TestPerClientIdentityLimits(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	for name, tt := range map[string]struct {
		policy  limit.Policy
		rate    float64
		burst   int
		allowed int
	}{
		"its limit, tighter than the policy's": {limit.Policy{Rate: 100, Burst: 200}, 1, 3, 3},
		"its limit, looser than the policy's":  {limit.Policy{Rate: 1, Burst: 2}, 10, 5, 5},
		"its limit, with none in the policy":   {limit.Policy{}, 1, 4, 4},
		"no limit of its own: the policy's":    {limit.Policy{Rate: 1, Burst: 2}, 0, 0, 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := limit.PerClientAt(tt.policy, identifyByHeader(tt.rate, tt.burst), clock, http.HandlerFunc(ok))
			for i := range tt.allowed {
				if code := getAs(h, "k", "192.0.2.1:1000"); code != 204 {
					t.Fatalf("request %d: %d, want 204", i+1, code)
				}
			}
			if code := getAs(h, "k", "192.0.2.1:1000"); code != 429 {
				t.Errorf("request %d: %d, want 429", tt.allowed+1, code)
			}
		})
	}
}

// TestPerClientIdentities checks who a client is: an identity is one client
// wherever it connects from, distinct from other identities and from an
// unidentified request on the same address, which is its address.
func TestPerClientIdentities(t *testing.T) {
	now := time.Unix(0, 0)
	h := limit.PerClientAt(limit.Policy{Rate: 1, Burst: 1}, identifyByHeader(0, 0), func() time.Time { return now }, http.HandlerFunc(ok))
	for _, step := range []struct {
		client, addr string
		want         int
	}{
		{"a", "192.0.2.1:1000", 204},
		{"a", "192.0.2.2:2000", 429}, // the same identity, from elsewhere
		{"b", "192.0.2.1:1000", 204}, // another identity, from a's address
		{"", "192.0.2.1:1000", 204},  // unidentified: the address, a client of its own
		{"", "192.0.2.1:3000", 429},  // the same address, another port
	} {
		if code := getAs(h, step.client, step.addr); code != step.want {
			t.Errorf("client %q from %s: %d, want %d", step.client, step.addr, code, step.want)
		}
	}
}
