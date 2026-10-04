package flags

import (
	"sync"
	"testing"
	"time"
)

func TestOutageTracker(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	o := newOutageTracker()
	o.now = func() time.Time { return clock }

	if ended, _, _ := o.answered(); ended {
		t.Error("answered() with no outage reported an ended outage")
	}

	if !o.failed() {
		t.Error("first failed() = false, want it to start an outage")
	}
	clock = clock.Add(2 * time.Second)
	if o.failed() || o.failed() {
		t.Error("failed() during an outage started a new one")
	}

	clock = clock.Add(3 * time.Second)
	ended, fallbacks, took := o.answered()
	if !ended || fallbacks != 3 || took != 5*time.Second {
		t.Errorf("answered() = %v, %d, %v, want true, 3, 5s", ended, fallbacks, took)
	}
	if ended, _, _ := o.answered(); ended {
		t.Error("a second answered() reported the outage ending again")
	}

	// A new outage starts counting from zero.
	if !o.failed() {
		t.Error("failed() after recovery = false, want a new outage")
	}
	if _, fallbacks, _ := o.answered(); fallbacks != 1 {
		t.Errorf("second outage fallbacks = %d, want 1", fallbacks)
	}
}

func TestOutageTrackerConcurrent(t *testing.T) {
	o := newOutageTracker()
	var wg sync.WaitGroup
	started := make(chan bool, 50)
	for range 50 {
		wg.Add(1)
		go func() { defer wg.Done(); started <- o.failed() }()
	}
	wg.Wait()
	close(started)
	n := 0
	for s := range started {
		if s {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d goroutines started the outage, want exactly 1", n)
	}
	if _, fallbacks, _ := o.answered(); fallbacks != 50 {
		t.Errorf("fallbacks = %d, want 50", fallbacks)
	}
}

func TestOnceSet(t *testing.T) {
	var s onceSet
	if !s.first("a") {
		t.Error(`first("a") = false, want true the first time`)
	}
	if s.first("a") {
		t.Error(`first("a") = true the second time`)
	}
	if !s.first("b") {
		t.Error(`first("b") = false; ids are independent`)
	}
	s.clear("a")
	if !s.first("a") {
		t.Error(`first("a") after clear = false, want true again`)
	}
	s.clear("never-seen") // must not panic on the zero map or a missing id
}
