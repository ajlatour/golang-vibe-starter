package featureflag

import (
	"sync"
	"time"
)

// outageTracker follows whether the flag service is answering, so the client
// can log one line when an outage starts and one when it ends instead of one
// per failed evaluation. It is safe for concurrent use.
type outageTracker struct {
	now func() time.Time // time.Now; replaced in tests

	mu        sync.Mutex
	active    bool
	since     time.Time // when the current outage was first seen
	fallbacks int       // evaluations answered from defaults during it
}

func newOutageTracker() *outageTracker {
	return &outageTracker{now: time.Now}
}

// failed records an evaluation that failed because the service didn't answer.
// It reports whether this one started a new outage.
func (o *outageTracker) failed() (started bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.active {
		o.active, o.since, o.fallbacks = true, o.now(), 0
		started = true
	}
	o.fallbacks++
	return started
}

// answered records that the service answered, whatever it said. If an outage
// was in progress it ends, and answered returns how many evaluations it cost
// and how long it lasted; otherwise ended is false.
func (o *outageTracker) answered() (ended bool, fallbacks int, took time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.active {
		return false, 0, 0
	}
	fallbacks, took = o.fallbacks, o.now().Sub(o.since)
	o.active, o.fallbacks = false, 0
	return true, fallbacks, took
}

// onceSet remembers which problems have been reported, so each is logged
// loudly once and quietly after that, until it clears. It is safe for
// concurrent use; the zero value is ready.
type onceSet struct {
	mu   sync.Mutex
	seen map[string]bool
}

// first reports whether id hasn't been reported since it was last cleared,
// and marks it reported.
func (s *onceSet) first(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[id] {
		return false
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[id] = true
	return true
}

// clear forgets id, so its next report is a first again.
func (s *onceSet) clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, id)
}
