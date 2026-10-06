package outage_test

import (
	"sync"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/outage"
)

func TestTracker(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	o := outage.New(func() time.Time { return clock })

	if ended, _, _ := o.Answered(); ended {
		t.Error("Answered with no outage reported an ended outage")
	}
	if !o.Failed() {
		t.Error("the first Failed didn't start an outage")
	}
	clock = clock.Add(2 * time.Second)
	for range 2 { // more failures count, but start nothing
		if o.Failed() {
			t.Error("Failed during an outage started a new one")
		}
	}
	clock = clock.Add(3 * time.Second)
	if ended, failures, took := o.Answered(); !ended || failures != 3 || took != 5*time.Second {
		t.Errorf("Answered = %v, %d, %v; want true, 3, 5s", ended, failures, took)
	}
	if ended, _, _ := o.Answered(); ended {
		t.Error("a second Answered reported the outage ending again")
	}
	if !o.Failed() {
		t.Error("Failed after recovery didn't start a new outage")
	}
	if _, failures, _ := o.Answered(); failures != 1 {
		t.Errorf("the second outage's failures = %d, want 1: counting starts again", failures)
	}
}

// TestWaiting checks that during an outage, one caller asks per retry
// interval and the rest wait; outside one, and with no retry, nobody waits.
func TestWaiting(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	o := outage.New(func() time.Time { return clock })
	if o.Waiting(time.Second) {
		t.Error("Waiting with no outage")
	}
	o.Failed()
	for i := range 2 { // every caller in that second
		if !o.Waiting(time.Second) {
			t.Errorf("caller %d: not Waiting within a second of the failure", i+1)
		}
	}
	if o.Waiting(0) {
		t.Error("Waiting with a retry of zero")
	}
	clock = clock.Add(time.Second)
	if o.Waiting(time.Second) {
		t.Error("Waiting a second after the failure: this caller should ask")
	}
	if !o.Waiting(time.Second) {
		t.Error("a second caller at the same moment didn't wait for the first's try")
	}
}

func TestTrackerConcurrent(t *testing.T) {
	o := outage.New(nil)
	var wg sync.WaitGroup
	started := make(chan bool, 50)
	for range 50 {
		wg.Go(func() { started <- o.Failed() })
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
	if _, failures, _ := o.Answered(); failures != 50 {
		t.Errorf("failures = %d, want 50", failures)
	}
}
