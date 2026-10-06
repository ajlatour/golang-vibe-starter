// Package outage follows whether a dependency the service calls on the
// request path is answering, so a client of it can do three things the same
// way: log one line when an outage starts and one when it ends, instead of
// one per failed call; count what the outage cost; and, while it lasts, ask
// the dependency at most once per retry interval, answering the calls in
// between at once instead of each waiting out a timeout.
//
// The flagd client (internal/featureflag) and the request validation
// middleware (internal/validator) both use it.
package outage

import (
	"sync"
	"time"
)

// Tracker follows one dependency. It is safe for concurrent use.
type Tracker struct {
	now func() time.Time

	mu       sync.Mutex
	active   bool
	since    time.Time // when the current outage was first seen
	tried    time.Time // when the dependency was last asked during it
	failures int       // calls answered without it during the outage
}

// New returns a Tracker that reads the time from now; nil means time.Now.
// Tests pass a clock they control.
func New(now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{now: now}
}

// Failed records a call the dependency didn't answer, or that wasn't asked
// because of the outage, and reports whether it started an outage.
func (t *Tracker) Failed() (started bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		t.active, t.since, t.failures = true, t.now(), 0
		t.tried = t.since
		started = true
	}
	t.failures++
	return started
}

// Waiting reports whether a call should do without the dependency, without
// asking it: an outage is on, and it was asked less than retry ago. When it
// reports false during an outage, the caller is the one to ask, and the next
// try is counted from now, so calls at the same moment keep waiting rather
// than all asking at once. A retry of zero never waits.
func (t *Tracker) Waiting(retry time.Duration) bool {
	if retry <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return false
	}
	now := t.now()
	if now.Sub(t.tried) < retry {
		return true
	}
	t.tried = now
	return false
}

// Answered records that the dependency answered, whatever it said. If that
// ended an outage, it reports how many calls the outage cost and how long it
// lasted.
func (t *Tracker) Answered() (ended bool, failures int, took time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return false, 0, 0
	}
	failures, took = t.failures, t.now().Sub(t.since)
	t.active, t.failures = false, 0
	return true, failures, took
}
