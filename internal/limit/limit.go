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

// Identity is who a request comes from, as whatever identified it knows: a
// name for the client, and the client's own rate limit, if it has one, which
// replaces the policy's (a zero Rate: the policy's).
type Identity struct {
	Client string
	Rate   float64
	Burst  int
}

// Identify tells the per-client limit who a request comes from, when it can:
// authentication, for one. It reports false for a request it can't identify.
// The limiter knows nothing of how: cmd/api supplies one from the API keys.
type Identify func(*http.Request) (Identity, bool)

// clientName names who a request comes from, for the per-client limit: the
// identity identify gave it, if any, else its connection's IP address. Proxy
// headers such as X-Forwarded-For are never trusted (the header guard removes
// them), so behind a proxy every unidentified request shares the proxy's
// address.
func clientName(r *http.Request, id Identity, identified bool) string {
	if identified {
		return "id:" + id.Client
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

type bucket struct {
	tokens      float64
	last        time.Time
	rate, burst float64 // the client's limit when the bucket was last used
}

// limiter holds a token bucket per client.
type limiter struct {
	now       func() time.Time
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

// take spends one of client's tokens if it has one. If not, it returns how
// long until it will.
func (l *limiter) take(client string, rate, burst float64) (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	b, found := l.buckets[client]
	if !found {
		b = &bucket{tokens: burst, last: now}
		l.buckets[client] = b
	}
	b.rate, b.burst = rate, burst // a reloaded key may have a new limit
	b.tokens = math.Min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / rate * float64(time.Second))
}

// sweep forgets, at most once a minute, the clients whose buckets have
// refilled: they're as if new, so keeping them only costs memory.
func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for c, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*b.rate >= b.burst {
			delete(l.buckets, c)
		}
	}
}

// PerClient refuses a request with 429 when its client is over its rate: the
// client identify names, with its own limit if it has one, else its IP
// address, with the policy's. A client's own limit applies even when the
// policy sets none. identify may be nil, and every client is then an IP
// address. Put it after whatever identify relies on: after authentication,
// so a client is its key.
func PerClient(p Policy, identify Identify, next http.Handler) http.Handler {
	return perClient(p, identify, time.Now, next)
}

func perClient(p Policy, identify Identify, now func() time.Time, next http.Handler) http.Handler {
	l := &limiter{now: now, buckets: map[string]*bucket{}, lastSweep: now()}
	exempt := exemptSet(p.Exempt)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		var id Identity
		identified := false
		if identify != nil {
			id, identified = identify(r)
		}
		rate, burst := p.Rate, float64(p.Burst)
		if identified && id.Rate > 0 {
			rate, burst = id.Rate, float64(id.Burst)
		}
		// With no limit, don't even name the client: it would cost every
		// request an allocation for nothing.
		if rate == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := l.take(clientName(r, id, identified), rate, burst); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			httpx.WriteError(w, http.StatusTooManyRequests, "too many requests; slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}
