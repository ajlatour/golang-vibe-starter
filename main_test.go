package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestRun starts the real server on a random port, makes a request, then
// cancels the context and checks that it shuts down cleanly.
func TestRun(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	env := map[string]string{"ADDR": "127.0.0.1:0"}
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		errc <- run(ctx, func(k string) string { return env[k] }, pw)
		pw.Close()
	}()

	// The first log line announces the bound address.
	var started struct {
		Msg  string `json:"msg"`
		Addr string `json:"addr"`
	}
	if err := json.NewDecoder(pr).Decode(&started); err != nil {
		t.Fatalf("reading startup log: %v (run error: %v)", err, <-errc)
	}
	go io.Copy(io.Discard, pr) // keep draining so logging never blocks
	if started.Msg != "server started" {
		t.Fatalf("first log msg = %q, want %q", started.Msg, "server started")
	}

	resp, err := http.Get("http://" + started.Addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("run() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return after context cancel")
	}
}

func TestRunConfigError(t *testing.T) {
	env := map[string]string{"LOG_LEVEL": "loud"}
	err := run(t.Context(), func(k string) string { return env[k] }, io.Discard)
	if err == nil {
		t.Fatal("run() error = nil, want config error")
	}
}
