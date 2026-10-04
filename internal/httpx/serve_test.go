package httpx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

// TestServe starts a real server on a random port, makes a request, then
// cancels the context and checks that it shuts down cleanly.
func TestServe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	pr, pw := io.Pipe()
	logger := slog.New(slog.NewJSONHandler(pw, nil))
	addr, shutdownTimeout := "127.0.0.1:0", 5*time.Second

	errc := make(chan error, 1)
	go func() {
		errc <- Serve(ctx, logger, addr, shutdownTimeout, http.HandlerFunc(Healthz))
		pw.Close()
	}()

	var started struct {
		Msg  string `json:"msg"`
		Addr string `json:"addr"`
	}
	if err := json.NewDecoder(pr).Decode(&started); err != nil {
		t.Fatalf("reading startup log: %v (Serve error: %v)", err, <-errc)
	}
	go io.Copy(io.Discard, pr) // keep draining so logging never blocks
	if started.Msg != "server started" {
		t.Fatalf("first log msg = %q, want %q", started.Msg, "server started")
	}

	resp, err := http.Get("http://" + started.Addr + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("Serve() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not return after context cancel")
	}
}

func TestServeListenError(t *testing.T) {
	addr, shutdownTimeout := "not-an-address", time.Second
	err := Serve(t.Context(), slog.New(slog.DiscardHandler), addr, shutdownTimeout, http.NotFoundHandler())
	if err == nil {
		t.Fatal("Serve() error = nil, want listen error")
	}
}
