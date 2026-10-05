package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// ReadyzPath is where services serve their Readiness.
const ReadyzPath = "/readyz"

// Check is one thing an instance needs before it can serve traffic.
type Check struct {
	Name string
	// Fn returns nil when the thing is available. It should be quick: its
	// context carries a short deadline.
	Fn func(context.Context) error
}

// Readiness answers /readyz: whether this instance should be sent traffic.
// Where /healthz says the process is alive (restart it if not), /readyz says
// whether to route requests to it: not while a check fails, and not once it
// has begun shutting down.
//
// Results are cached for a TTL, so frequent probes, from several load
// balancers at once, cost one round of checks per TTL. Changes are logged
// once each way, as "not ready" and "ready again".
type Readiness struct {
	logger   *slog.Logger
	checks   []Check
	ttl      time.Duration
	timeout  time.Duration
	stopping atomic.Bool

	mu      sync.Mutex
	checked time.Time
	failed  string // the failing check's name, "" when all passed
	ready   bool
	known   bool // whether ready has been reported yet
}

// NewReadiness returns a Readiness that runs checks at most once per ttl,
// each with a deadline of timeout.
func NewReadiness(logger *slog.Logger, ttl, timeout time.Duration, checks ...Check) *Readiness {
	return &Readiness{logger: logger, checks: checks, ttl: ttl, timeout: timeout}
}

// Stopping marks the instance as shutting down: from now on it is not ready,
// whatever the checks say.
func (rd *Readiness) Stopping() {
	if !rd.stopping.Swap(true) {
		rd.logger.Warn("not ready", logx.KeyReason, "shutting down")
	}
}

// status runs the checks if the cached result is older than the TTL, and
// returns the name of the failing check ("" if none).
func (rd *Readiness) status(ctx context.Context) string {
	rd.mu.Lock()
	defer rd.mu.Unlock()
	if rd.known && time.Since(rd.checked) < rd.ttl {
		return rd.failed
	}
	failed := ""
	var err error
	for _, c := range rd.checks {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rd.timeout)
		err = c.Fn(cctx)
		cancel()
		if err != nil {
			failed = c.Name
			break
		}
	}
	ready := failed == ""
	switch {
	case rd.known && ready && !rd.ready:
		rd.logger.Info("ready again")
	case (!rd.known || rd.ready) && !ready:
		rd.logger.Warn("not ready", logx.KeyReason, failed+" unavailable", logx.KeyError, err)
	}
	rd.checked, rd.failed, rd.ready, rd.known = time.Now(), failed, ready, true
	return failed
}

func (rd *Readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rd.stopping.Load() {
		WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "reason": "shutting down"})
		return
	}
	if failed := rd.status(r.Context()); failed != "" {
		WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready", "reason": failed + " unavailable"})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
