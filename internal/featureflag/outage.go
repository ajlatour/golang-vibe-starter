package featureflag

import "sync"

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
