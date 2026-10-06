package limit

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestSweepForgetsIdleClients checks memory doesn't grow with every client
// ever seen: once a client's bucket has refilled, it's forgotten.
func TestSweepForgetsIdleClients(t *testing.T) {
	now := time.Unix(0, 0)
	l := &limiter{now: func() time.Time { return now }, maxClients: MaxClients, buckets: map[string]*bucket{}, lastSweep: now}
	for i := range 1000 {
		l.take(fmt.Sprintf("ip:10.0.%d.%d", i/256, i%256), 10, 10)
	}
	if len(l.buckets) != 1000 {
		t.Fatalf("%d buckets, want 1000", len(l.buckets))
	}
	now = now.Add(2 * time.Minute)
	l.take("ip:10.9.9.9", 10, 10)
	if len(l.buckets) != 1 {
		t.Errorf("after two quiet minutes, %d buckets, want only the new client's", len(l.buckets))
	}
}

// TestFullLimiterSharesOneLimit checks the limiter never holds more than
// maxClients clients: when full, it forgets idle ones at once, and if none
// are idle, new clients share the overflow limit.
func TestFullLimiterSharesOneLimit(t *testing.T) {
	now := time.Unix(0, 0)
	l := &limiter{now: func() time.Time { return now }, maxClients: 3, buckets: map[string]*bucket{}, lastSweep: now}
	for _, c := range []string{"a", "b", "c"} {
		l.take(c, 1, 1) // each spends its only token: none is idle
	}
	// Full, nobody idle: d and e share the overflow limit, whose one token
	// d spends, so e is refused.
	if ok, _ := l.take("d", 1, 1); !ok {
		t.Error("the first newcomer while full was refused: the overflow limit should have a token")
	}
	if ok, _ := l.take("e", 1, 1); ok {
		t.Error("a second newcomer while full got through: newcomers should share the overflow limit")
	}
	if n := len(l.buckets); n > 4 {
		t.Errorf("%d buckets, want at most the 3 clients and the overflow", n)
	}

	// Once a client has refilled, a newcomer takes its place.
	now = now.Add(2 * time.Second)
	if ok, _ := l.take("f", 1, 1); !ok {
		t.Error("a newcomer once clients were idle was refused")
	}
	if _, ok := l.buckets["f"]; !ok {
		t.Error("the newcomer didn't get a limit of its own once there was room")
	}
}

// TestIPv6ClientsAreTheir64 checks an IPv6 client is its /64, whatever
// address in it it uses, and an IPv4 client its address.
func TestIPv6ClientsAreTheir64(t *testing.T) {
	name := func(addr string) string {
		r, _ := http.NewRequest(http.MethodGet, "/items", nil)
		r.RemoteAddr = addr
		return clientName(r, Identity{}, false)
	}
	a := name("[2001:db8:1:2::1]:443")
	if b := name("[2001:db8:1:2:ffff:ffff:ffff:ffff]:443"); a != b {
		t.Errorf("two addresses in one /64 are different clients: %s, %s", a, b)
	}
	if c := name("[2001:db8:1:3::1]:443"); c == a {
		t.Errorf("two /64s are one client: %s", c)
	}
	if v4 := name("192.0.2.1:443"); v4 != "ip:192.0.2.1" {
		t.Errorf("IPv4 client = %s, want its address", v4)
	}
	if mapped := name("[::ffff:192.0.2.1]:443"); mapped == a || mapped == "ip6:::/64" {
		t.Errorf("an IPv4-mapped address was taken as an IPv6 /64: %s", mapped)
	}
}
