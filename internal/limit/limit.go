// Package limit keeps one busy client, or a flood, from taking the service
// and its upstream down with it: a cap on requests in flight across all
// clients, and a token-bucket rate limit per client.
//
// Both refuse at once rather than queue: a refused request costs the service
// almost nothing, and its Retry-After header tells a well-behaved client when
// to try again. Neither is a substitute for limits in front of the service
// (a load balancer, a gateway); they're what the service does for itself.
package limit

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// Policy is the limits' configuration.
type Policy struct {
	// MaxInFlight caps the requests being served at once, across all
	// clients. Over it, a request gets 503. Zero is no cap.
	MaxInFlight int
	// Rate is the requests per second each client may make on average, and
	// Burst how many it may make at once after a quiet spell. Over them, a
	// request gets 429. A zero Rate is no per-client limit.
	Rate  float64
	Burst int
	// Exempt lists paths neither limit applies to, such as the healthcheck,
	// which must answer however busy the service is.
	Exempt []string
}

// Validate reports a policy the limits can't work with.
func (p Policy) Validate() error {
	switch {
	case p.MaxInFlight < 0:
		return fmt.Errorf("MAX_IN_FLIGHT must be 0 (no cap) or more, got %d", p.MaxInFlight)
	case p.Rate < 0 || math.IsInf(p.Rate, 0) || math.IsNaN(p.Rate):
		return fmt.Errorf("RATE_LIMIT must be 0 (no limit) or more, got %v", p.Rate)
	case p.Rate > 0 && p.Burst < 1:
		return fmt.Errorf("RATE_BURST must be at least 1 when RATE_LIMIT is set, got %d", p.Burst)
	}
	return nil
}

func exemptSet(paths []string) map[string]bool {
	m := map[string]bool{}
	for _, p := range paths {
		m[p] = true
	}
	return m
}

// InFlight refuses a request with 503 while MaxInFlight requests are being
// served. Put it outermost, before authentication, so floods of requests
// with no valid key are capped too.
func InFlight(p Policy, next http.Handler) http.Handler {
	if p.MaxInFlight == 0 {
		return next
	}
	slots := make(chan struct{}, p.MaxInFlight)
	exempt := exemptSet(p.Exempt)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			httpx.WriteError(w, http.StatusServiceUnavailable, "the service is busy; try again shortly")
		}
	})
}

// Client names who a request comes from, for the per-client limit: its API
// key's name when it was authenticated with one, else its connection's IP
// address. Proxy headers such as X-Forwarded-For are never trusted (the
// header guard removes them), so behind a proxy every keyless request shares
// the proxy's address.
func Client(r *http.Request) string {
	if name := apikey.Name(r.Context()); name != "" {
		return "key:" + name
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

type bucket struct {
	tokens float64
	last   time.Time
}

// limiter holds a token bucket per client.
type limiter struct {
	rate      float64
	burst     float64
	now       func() time.Time
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

// take spends one of client's tokens if it has one. If not, it returns how
// long until it will.
func (l *limiter) take(client string) (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	b, found := l.buckets[client]
	if !found {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[client] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweep forgets, at most once a minute, the clients whose buckets have
// refilled: they're as if new, so keeping them only costs memory.
func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for c, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, c)
		}
	}
}

// PerClient refuses a request with 429 when its client (see Client) is over
// its rate. Put it after authentication, so a client is its key.
func PerClient(p Policy, next http.Handler) http.Handler {
	return perClient(p, time.Now, next)
}

func perClient(p Policy, now func() time.Time, next http.Handler) http.Handler {
	if p.Rate == 0 {
		return next
	}
	l := &limiter{rate: p.Rate, burst: float64(p.Burst), now: now, buckets: map[string]*bucket{}, lastSweep: now()}
	exempt := exemptSet(p.Exempt)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := l.take(Client(r)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			httpx.WriteError(w, http.StatusTooManyRequests, "too many requests; slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}
