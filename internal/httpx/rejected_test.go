package httpx_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

// TestLogRejected runs a real server and sends it requests it answers
// itself, before the handler: each must be logged once, and counted, while
// requests the handler sees, and connections that never sent a byte, aren't.
func TestLogRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var logs logtest.Buffer
	var count atomic.Int32
	addrc := make(chan string, 1)
	done := make(chan error, 1)
	// Serve logs "server started" with the address; read it from there.
	logger := slog.New(logs.Handler(slog.LevelDebug))
	go func() {
		done <- httpx.Serve(ctx, logger, "127.0.0.1:0", time.Second, http.HandlerFunc(httpx.Healthz),
			httpx.MaxHeaderBytes(4<<10), httpx.LogRejected(true, func() { count.Add(1) }))
	}()
	defer func() { cancel(); <-done }()
	go func() {
		for {
			for _, r := range logs.Records(t) {
				if r["msg"] == "server started" {
					addrc <- r["addr"].(string)
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	addr := <-addrc

	// send writes raw to a new connection, reads what comes back until the
	// server closes it, and returns the first line.
	send := func(raw string) string {
		t.Helper()
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_, _ = io.WriteString(c, raw)
		_ = c.SetReadDeadline(time.Now().Add(8 * time.Second)) // past the 5s read-header timeout
		line, _ := bufio.NewReader(c).ReadString('\n')
		return strings.TrimSpace(line)
	}
	// rejected waits for the lines a closing connection logs: the server
	// can wait half a second before closing one, so the client's last
	// bytes arrive before the close.
	rejected := func(want int) []map[string]any {
		var out []map[string]any
		wait := 2 * time.Second
		if want == 0 {
			wait = 700 * time.Millisecond // past the server's delay before closing
		}
		for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			out = out[:0]
			for _, r := range logs.Records(t) {
				if r["msg"] == "request rejected by the server" {
					out = append(out, r)
				}
			}
			if want > 0 && len(out) >= want {
				break
			}
		}
		return out
	}

	var before int32
	for _, tc := range []struct{ name, raw, status string }{
		{"malformed request line", "GARBAGE\r\n\r\n", "400"},
		{"invalid header name", "GET / HTTP/1.1\r\nHost: x\r\nBad Header: y\r\n\r\n", "400"},
		{"headers over the limit", "GET / HTTP/1.1\r\nHost: x\r\nX-Big: " + strings.Repeat("a", 8<<10) + "\r\n\r\n", "431"},
		{"unsupported transfer-encoding", "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: gzip\r\n\r\n", "501"},
		// Headers that never finish: after the read-header timeout, the
		// server closes the connection without a word.
		{"headers too slow", "GET / HTTP/1.1\r\nHost: x\r\n", ""},
	} {
		logs.Reset()
		before = count.Load()
		if got := send(tc.raw); !strings.Contains(got, tc.status) {
			t.Errorf("%s: got %q, want %s", tc.name, got, tc.status)
		}
		recs := rejected(1)
		if len(recs) != 1 || count.Load() != before+1 {
			t.Errorf("%s: %d lines, counted %d; want 1 of each:\n%s", tc.name, len(recs), count.Load()-before, logs.String())
			continue
		}
		if r := recs[0]; r["level"] != "WARN" || r["client_ip"] != "127.0.0.1" || r["duration_ms"] == nil {
			t.Errorf("%s: logged %v", tc.name, r)
		}
	}

	// A good request, then, on the same connection once it's answered, a
	// bad one: only the second is the server's.
	logs.Reset()
	before = count.Load()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("first request: %v, %v", resp, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_, _ = io.WriteString(c, "GARBAGE\r\n\r\n")
	if line, _ := br.ReadString('\n'); !strings.Contains(line, "400") {
		t.Errorf("second request: %q, want 400", line)
	}
	c.Close()
	if recs := rejected(1); len(recs) != 1 || count.Load() != before+1 {
		t.Errorf("a bad request on a kept-alive connection: %d lines, counted %d", len(recs), count.Load()-before)
	}

	// Not the server's: a request the handler answers, and a connection
	// that never sent anything.
	logs.Reset()
	before = count.Load()
	if got := send("GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"); !strings.Contains(got, "200") {
		t.Errorf("good request: %q", got)
	}
	c, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	// A request answered without its body being read (as a 401 would be):
	// the server reads what's left, or gives up and closes, after the
	// handler, and those bytes were seen with their request.
	big := strings.Repeat("x", 100<<10) // under the 256 KiB the server drains to keep the connection
	if got := send(fmt.Sprintf("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n\r\n%s", len(big), big)); !strings.Contains(got, "200") {
		t.Errorf("large body: %q", got)
	}
	if recs := rejected(0); len(recs) != 0 || count.Load() != before {
		t.Errorf("logged what the handler saw, or an empty connection: %v", recs)
	}
}
