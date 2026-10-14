package apikey_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

func TestParseKeysLimitsAndLayout(t *testing.T) {
	h := apikey.HashHex("k")
	keys, err := apikey.ParseKeys("# ci keys\n a:write:" + h + ":500/1000\n b:read:" + h + ":2.5, c:read:" + h + "\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("%d keys, want 3: %+v", len(keys), keys)
	}
	for i, want := range []struct {
		rate  float64
		burst int
	}{{500, 1000}, {2.5, 5}, {0, 0}} {
		if keys[i].Rate != want.rate || keys[i].Burst != want.burst {
			t.Errorf("key %s: rate %v burst %d, want %v and %d", keys[i].Name, keys[i].Rate, keys[i].Burst, want.rate, want.burst)
		}
	}
	if got := apikey.Describe(keys); got != "a (write, 500/s in bursts of 1000), b (read, 2.5/s in bursts of 5), c (read)" {
		t.Errorf("Describe = %q", got)
	}
	for _, bad := range []string{"a:write:" + h + ":0", "a:write:" + h + ":-1", "a:write:" + h + ":x", "a:write:" + h + ":5/0", "a:write:" + h + ":5/x", "a:write:" + h + ":5:6"} {
		if _, err := apikey.ParseKeys(bad); err == nil {
			t.Errorf("ParseKeys(%q) = nil error, want one", bad)
		}
	}
}

// TestWatchFile checks a keys file is reloaded when it changes, and that a bad
// edit, an emptied file or a deleted one leaves the keys in force alone, each
// logged once.
func TestWatchFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys")
	// Each version of the file replaces the last in one step, as a mounted
	// secret is swapped. os.WriteFile empties the file before writing it, and
	// a test paused between the two for longer than two checks (a busy runner,
	// the race detector) would let the watcher see an empty file settle: not
	// acted on, but logged, which the exact log count below would trip over.
	// The watcher's handling of a file caught mid-write is in watch_test.go.
	write := func(content string) {
		t.Helper()
		tmp := filepath.Join(dir, "keys.tmp")
		if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(name, key string) string { return name + ":write:" + apikey.HashHex(key) + "\n" }
	write(entry("first", "gvs_one"))
	keys, err := apikey.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ring := apikey.NewKeyring(keys)

	var logs logtest.Buffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		apikey.WatchFile(ctx, logs.Logger(slog.LevelDebug), path, ring, 10*time.Millisecond)
		close(done)
	}()
	defer func() { cancel(); <-done }()

	names := func() string { return apikey.Describe(ring.Keys()) }
	waitFor := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if names() == want {
				return
			}
		}
		t.Fatalf("keys in force = %q, want %q", names(), want)
	}
	// failures waits until the watcher has logged n refusals: each bad state
	// below must be seen and logged, however slow the runner, before the
	// next replaces it.
	failures := func(n int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if strings.Count(logs.String(), "API keys not reloaded") >= n {
				return
			}
		}
		t.Fatalf("want %d \"API keys not reloaded\" lines; logged:\n%s", n, logs.String())
	}

	write(entry("first", "gvs_one") + entry("second", "gvs_two"))
	waitFor("first (write), second (write)")

	write("first:admin:nope\n")
	failures(1)
	write("first:admin:nope\n") // the same bad file again: logged once
	// Time for the watcher to see it, so the count below shows it wasn't
	// logged again; a slow runner that misses it fails nothing.
	time.Sleep(100 * time.Millisecond)
	write("# nothing\n")
	failures(2)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	failures(3)
	if names() != "first (write), second (write)" {
		t.Fatalf("a bad, empty or missing file changed the keys in force: %q", names())
	}

	write(entry("third", "gvs_three"))
	waitFor("third (write)")
	// The watcher logs a reload just after putting the keys in force: stop it,
	// and wait for it to return, so its last line is written before reading.
	cancel()
	<-done

	var got []string
	for _, r := range logs.Records(t) {
		got = append(got, r["level"].(string)+" "+r["msg"].(string))
	}
	want := []string{"INFO API keys reloaded", "ERROR API keys not reloaded", "ERROR API keys not reloaded", "ERROR API keys not reloaded", "INFO API keys reloaded"}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("logs:\n %q\nwant\n %q", got, want)
	}
	if strings.Contains(logs.String(), "gvs_") {
		t.Error("a key was logged")
	}
}
