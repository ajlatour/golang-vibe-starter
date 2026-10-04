package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

// startApp runs the service in the background with env and waits for its
// "server started" log line. It returns the bound address (use ADDR
// "127.0.0.1:0" in env for a free port), everything the service logs, and a
// channel that receives run's result.
func startApp(t *testing.T, ctx context.Context, env map[string]string) (addr string, logs *logtest.Buffer, errc <-chan error) {
	t.Helper()
	logs = &logtest.Buffer{}
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, getenvFrom(env), logs) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("run() exited before starting: %v\nlogs: %s", err, logs)
		default:
		}
		for _, line := range logs.Lines() {
			var rec struct {
				Msg  string `json:"msg"`
				Addr string `json:"addr"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Msg == "server started" {
				return rec.Addr, logs, done
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not start; logs: %s", logs)
	return "", nil, nil
}

// waitForShutdown fails the test unless run returns nil within a few
// seconds. Cancel the context passed to startApp first.
func waitForShutdown(t *testing.T, errc <-chan error) {
	t.Helper()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("run() error = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return after context cancel")
	}
}

func getenvFrom(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}
