package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// TestRun starts the mock on a random port, checks it serves the seed data
// and answers the healthcheck subcommand, then checks for a clean shutdown.
func TestRun(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	addr, errc := startRun(t, ctx, map[string]string{"ADDR": "127.0.0.1:0"})

	t.Run("serves seed items", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/items")
		if err != nil {
			t.Fatalf("GET /items: %v", err)
		}
		defer resp.Body.Close()
		var got struct{ Items []item.Item }
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Items) != len(seedItems) {
			t.Errorf("got %d items, want %d", len(got.Items), len(seedItems))
		}
	})

	t.Run("healthcheck subcommand", func(t *testing.T) {
		if err := run(t.Context(), []string{"healthcheck"}, getenvFrom(map[string]string{"ADDR": addr}), io.Discard); err != nil {
			t.Errorf("healthcheck error = %v, want nil", err)
		}
	})

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

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "bad config", env: map[string]string{"LOG_LEVEL": "loud"}},
		{name: "unknown argument", args: []string{"serve"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := run(t.Context(), tt.args, getenvFrom(tt.env), io.Discard); err == nil {
				t.Error("run() error = nil, want error")
			}
		})
	}
}

// startRun runs the service in the background and waits for its
// "server started" log line, returning the bound address and a channel that
// receives run's result.
func startRun(t *testing.T, ctx context.Context, env map[string]string) (string, <-chan error) {
	t.Helper()
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		errc <- run(ctx, nil, getenvFrom(env), pw)
		pw.Close()
	}()

	dec := json.NewDecoder(pr)
	for {
		var line struct {
			Msg  string `json:"msg"`
			Addr string `json:"addr"`
		}
		if err := dec.Decode(&line); err != nil {
			t.Fatalf("reading logs: %v (run error: %v)", err, <-errc)
		}
		if line.Msg == "server started" {
			go io.Copy(io.Discard, pr) // keep draining so logging never blocks
			return line.Addr, errc
		}
	}
}

func getenvFrom(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}
