package profiling

import (
	"net"
	"testing"
)

func TestLoopbackOnly(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:6061": true, "[::1]:6061": true,
		"0.0.0.0:6061": false, "[::]:6061": false, "192.168.0.14:6061": false,
	} {
		a, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := loopbackOnly(a); got != want {
			t.Errorf("loopbackOnly(%s) = %v, want %v", addr, got, want)
		}
	}
}
