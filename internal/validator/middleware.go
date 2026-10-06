package validator

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// Policy is how the middleware behaves.
type Policy struct {
	// FailOpen lets a request through when the service can't answer. The
	// default, false, refuses it with 503: a check the API can't serve
	// without shouldn't be skipped quietly.
	FailOpen bool
	// Retry is how long, while the service is down, before it's asked
	// again; requests in between take the failure mode at once. Zero asks
	// it every time.
	Retry time.Duration
	// Exempt lists paths never checked, such as the probes.
	Exempt []string
	// Client names who a request comes from, for Request.Client: the API
	// key's name, say. Nil leaves it empty.
	Client func(*http.Request) string
	// Enforce says whether to act on the service's answers for a request.
	// When it says false, the service is still asked, and what it says is
	// logged and observed, but every request goes through: shadow mode, to
	// deploy the check dark and see what it would do. cmd/api backs it
	// with the request-validation-enforce feature flag. Nil enforces.
	Enforce func(context.Context) bool
	// Observe, if set, is told each checked request's outcome (see
	// Outcome), and how long the call to the service took (zero when it
	// wasn't asked), for metrics.
	Observe func(o Outcome, call time.Duration)
}

// FlagEnforce is the feature flag cmd/api backs Policy.Enforce with: off,
// its default, is shadow mode. Turning it on enforces the service's answers,
// at runtime, and off again rolls back.
const FlagEnforce = "request-validation-enforce"

// FlagKeys lists the feature flags this package's checks are run by. The
// service checks at startup that flagd/flags.json defines each.
func FlagKeys() []string { return []string{FlagEnforce} }

// Outcome is what the check came to for a request.
type Outcome string

const (
	// Allowed: the service allowed the request.
	Allowed Outcome = "allowed"
	// Refused: the service refused it, and the refusal was enforced (403).
	Refused Outcome = "refused"
	// ShadowRefused: the service refused it in shadow mode, so it went
	// through.
	ShadowRefused Outcome = "shadow_refused"
	// Unavailable: the service gave no usable answer; the failure mode
	// applied.
	Unavailable Outcome = "unavailable"
	// NotAsked: during an outage, the service wasn't asked again yet; the
	// failure mode applied.
	NotAsked Outcome = "not_asked"
)

// Outcomes lists every Outcome, for metrics that report each.
var Outcomes = []Outcome{Allowed, Refused, ShadowRefused, Unavailable, NotAsked}

// The messages a client gets. Neither says why the service refused, or
// failed: that's logged.
const (
	refusedMessage     = "the request was refused"
	unavailableMessage = "request validation is unavailable; try again shortly"
)

// Middleware asks c about every request before next sees it: a request the
// service allows goes through; one it refuses gets 403; one it can't answer
// about gets the failure mode, 503 (fail closed) or through (fail open). In
// shadow mode (Policy.Enforce false) every request goes through, and the
// refusals and outages are only logged.
// Put it after authentication and the rate limit, so it knows who's calling
// and a client's flood doesn't reach the service.
//
// Each refusal is logged once, with the service's reason. An outage logs one
// warning when it starts and one line, with what it cost, when it ends.
func Middleware(logger *slog.Logger, c Checker, p Policy, next http.Handler) http.Handler {
	return middleware(logger, c, p, time.Now, next)
}

func middleware(logger *slog.Logger, c Checker, p Policy, now func() time.Time, next http.Handler) http.Handler {
	exempt := map[string]bool{}
	for _, path := range p.Exempt {
		exempt[path] = true
	}
	o := &outage{now: now}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		req := Request{Method: r.Method, Path: r.URL.Path}
		if p.Client != nil {
			req.Client = p.Client(r)
		}

		enforce := p.Enforce == nil || p.Enforce(r.Context())
		observe := func(o Outcome, call time.Duration) {
			if p.Observe != nil {
				p.Observe(o, call)
			}
		}

		var d Decision
		var err error
		var call time.Duration
		if o.waiting(p.Retry) {
			err = errNotRetried
		} else {
			start := now()
			d, err = c.Check(r.Context(), req)
			call = now().Sub(start)
		}
		if err != nil {
			if r.Context().Err() != nil {
				return // the client left: nobody to answer
			}
			if err == errNotRetried {
				observe(NotAsked, 0)
			} else {
				observe(Unavailable, call)
			}
			if o.failed() {
				logger.LogAttrs(r.Context(), slog.LevelWarn, "request validation unavailable",
					slog.Bool(logx.KeyEnforcing, enforce), slog.Bool(logx.KeyFailOpen, p.FailOpen), slog.Any(logx.KeyError, err))
			}
			if !enforce || p.FailOpen {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Retry-After", "1")
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeValidationUnavailable, unavailableMessage)
			return
		}
		if ended, failures, took := o.answered(); ended {
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request validation recovered",
				slog.Int(logx.KeyFailures, failures), logx.DurationMS(took))
		}
		switch {
		case d.Allow:
			observe(Allowed, call)
		case !enforce:
			observe(ShadowRefused, call)
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request would be refused by validation",
				slog.String(logx.KeyReason, logx.Truncate(d.Reason, 200)))
		default:
			observe(Refused, call)
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request refused by validation",
				slog.String(logx.KeyReason, logx.Truncate(d.Reason, 200)))
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeValidationRefused, refusedMessage)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// errNotRetried is the failure of a request that, during an outage, didn't
// ask the service: it was asked less than Policy.Retry ago.
var errNotRetried = errorString("request validation unavailable; not asked again yet")

type errorString string

func (e errorString) Error() string { return string(e) }

// outage follows whether the service is answering: one warning when an
// outage starts, one line when it ends, and at most one try per retry
// interval in between. Safe for concurrent use.
type outage struct {
	now func() time.Time

	mu       sync.Mutex
	active   bool
	since    time.Time // when the outage was first seen
	tried    time.Time // when the service was last asked during it
	failures int       // requests it answered with the failure mode
}

// failed records a request the service didn't answer about, and reports
// whether it started an outage.
func (o *outage) failed() (started bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.active {
		o.active, o.since, o.tried, o.failures = true, o.now(), o.now(), 0
		started = true
	}
	o.failures++
	return started
}

// waiting reports whether a request should take the failure mode without
// asking: an outage is on, and the service was asked less than retry ago.
// When it reports false during an outage, the caller is the one to ask.
func (o *outage) waiting(retry time.Duration) bool {
	if retry <= 0 {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.active {
		return false
	}
	now := o.now()
	if now.Sub(o.tried) < retry {
		return true
	}
	o.tried = now
	return false
}

// answered records that the service answered, and if that ended an outage,
// what it cost.
func (o *outage) answered() (ended bool, failures int, took time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.active {
		return false, 0, 0
	}
	o.active = false
	return true, o.failures, o.now().Sub(o.since)
}
