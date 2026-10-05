package profiling_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/profiling"
)

func TestHandlerServesTheProfiles(t *testing.T) {
	srv := httptest.NewServer(profiling.Handler())
	defer srv.Close()
	for path, want := range map[string]string{
		"/debug/pprof/":                     "Types of profiles available",
		"/debug/pprof/goroutine?debug=1":    "goroutine profile:",
		"/debug/pprof/heap?debug=1":         "heap profile:",
		"/debug/pprof/allocs?debug=1":       "heap profile:",
		"/debug/pprof/cmdline":              "profiling.test",
		"/debug/pprof/profile?seconds=1":    "", // binary; just has to arrive
		"/debug/pprof/trace?seconds=0.1":    "",
		"/debug/pprof/threadcreate?debug=1": "threadcreate profile:",
	} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(srv.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK || len(body) == 0 {
				t.Fatalf("status %d, %d bytes; want 200 and a profile", resp.StatusCode, len(body))
			}
			if !strings.Contains(string(body), want) {
				t.Errorf("body lacks %q", want)
			}
		})
	}
}

// TestServeStreamsALongProfile checks a CPU profile runs as long as asked:
// the server has no write timeout like the API's 15s to cut it off. It asks
// for 2s.
func TestServeStreamsALongProfile(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var logs logtest.Buffer
	done := make(chan struct{})
	go func() {
		profiling.Serve(ctx, logs.Logger(slog.LevelDebug), ln)
		close(done)
	}()

	start := time.Now()
	resp, err := http.Get("http://" + ln.Addr().String() + "/debug/pprof/profile?seconds=2")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("status %d, %d bytes, err %v; want a whole profile", resp.StatusCode, len(body), err)
	}
	if took := time.Since(start); took < 2*time.Second {
		t.Errorf("the profile took %s, want the 2s asked for", took)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve didn't return after the context was cancelled")
	}
	recs := logs.Records(t)
	if len(recs) != 1 || recs[0]["msg"] != "profiling server started" || recs[0]["addr"] != ln.Addr().String() {
		t.Errorf("logs = %v, want one profiling server started with the address", recs)
	}
}

func TestSecondsAreCapped(t *testing.T) {
	srv := httptest.NewServer(profiling.Handler())
	defer srv.Close()
	for _, path := range []string{"/debug/pprof/profile", "/debug/pprof/trace"} {
		resp, err := http.Get(srv.URL + path + "?seconds=61")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s?seconds=61: status %d, want 400", path, resp.StatusCode)
		}
	}
}
