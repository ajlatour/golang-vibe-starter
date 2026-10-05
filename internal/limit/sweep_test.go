package limit

import (
	"fmt"
	"testing"
	"time"
)

// TestSweepForgetsIdleClients checks memory doesn't grow with every client
// ever seen: once a client's bucket has refilled, it's forgotten.
func TestSweepForgetsIdleClients(t *testing.T) {
	now := time.Unix(0, 0)
	l := &limiter{rate: 10, burst: 10, now: func() time.Time { return now }, buckets: map[string]*bucket{}, lastSweep: now}
	for i := range 1000 {
		l.take(fmt.Sprintf("ip:10.0.%d.%d", i/256, i%256))
	}
	if len(l.buckets) != 1000 {
		t.Fatalf("%d buckets, want 1000", len(l.buckets))
	}
	now = now.Add(2 * time.Minute)
	l.take("ip:10.9.9.9")
	if len(l.buckets) != 1 {
		t.Errorf("after two quiet minutes, %d buckets, want only the new client's", len(l.buckets))
	}
}
