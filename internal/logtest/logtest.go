// Package logtest provides test helpers for code that logs with log/slog: it
// captures JSON log output and reads it back as records. It is imported only
// by tests, and imports nothing from this module, so any package's tests,
// including internal ones, can use it.
package logtest

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// Buffer collects log output. It is safe for concurrent use, so a service
// running in another goroutine can log to it while the test reads. The zero
// value is ready to use.
type Buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write implements io.Writer.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

// String returns everything logged so far.
func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// Len returns the number of bytes logged so far.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Len()
}

// Reset discards everything logged so far.
func (b *Buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.b.Reset()
}

// Handler returns a JSON handler writing to b at the given level. Wrap it
// (for example with logx.NewContextHandler) to get more than the base fields.
func (b *Buffer) Handler(level slog.Level) slog.Handler {
	return slog.NewJSONHandler(b, &slog.HandlerOptions{Level: level})
}

// Logger returns a logger on b's Handler.
func (b *Buffer) Logger(level slog.Level) *slog.Logger {
	return slog.New(b.Handler(level))
}

// Lines returns the non-empty lines logged so far, raw.
func (b *Buffer) Lines() []string {
	var out []string
	for _, line := range strings.Split(b.String(), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Records parses each logged line as a JSON record, failing the test on a
// line that isn't JSON.
func (b *Buffer) Records(t testing.TB) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range b.Lines() {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", line, err)
		}
		out = append(out, m)
	}
	return out
}

// WaitFor waits up to timeout for a record with msg to be logged, and
// returns the first; it fails the test if none is. For a server started in
// the background, its "server started" line says when it's listening, and
// where (addr).
func (b *Buffer) WaitFor(t testing.TB, msg string, timeout time.Duration) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(5 * time.Millisecond) {
		for _, r := range b.Records(t) {
			if r["msg"] == msg {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q line within %v; logged:\n%s", msg, timeout, b.String())
			return nil
		}
	}
}

// Msgs returns the "msg" of each record, in order.
func Msgs(recs []map[string]any) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r["msg"].(string))
	}
	return out
}

// LevelMsgs returns "LEVEL msg" for each record, in order, to assert on both
// at once.
func LevelMsgs(recs []map[string]any) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r["level"].(string)+" "+r["msg"].(string))
	}
	return out
}
