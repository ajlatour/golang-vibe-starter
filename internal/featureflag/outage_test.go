package featureflag

import (
	"testing"
)

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
