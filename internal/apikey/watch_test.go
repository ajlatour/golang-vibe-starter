package apikey

import (
	"log/slog"
	"testing"
)

// TestWatcherIgnoresTornReads drives the watcher one check at a time: a file
// caught half-written, here a truncated list that still parses, must never
// replace the keys in force, however it reads.
func TestWatcherIgnoresTornReads(t *testing.T) {
	entry := func(name string) string { return name + ":read:" + HashHex("gvs_"+name) + "\n" }
	full := []byte(entry("a") + entry("b"))
	torn := []byte(entry("a")) // as if caught mid-write: valid, and missing b

	keys, err := ParseKeys(string(full))
	if err != nil {
		t.Fatal(err)
	}
	ring := NewKeyring(keys)
	w := fileWatcher{logger: slog.New(slog.DiscardHandler), ring: ring, last: full, seen: full}
	inForce := func() string { return Describe(ring.Keys()) }
	want := inForce()

	// Seen once between two full readings: never acted on.
	w.check(torn, nil)
	w.check(full, nil)
	if got := inForce(); got != want {
		t.Fatalf("a torn read, seen once, replaced the keys: %q", got)
	}

	// A real change, read the same twice: acted on, once.
	w.check(torn, nil)
	if got := inForce(); got != want {
		t.Fatalf("a change was acted on after one reading: %q", got)
	}
	w.check(torn, nil)
	if got := inForce(); got != "a (read)" {
		t.Fatalf("a change read twice wasn't acted on: keys are %q", got)
	}
}
