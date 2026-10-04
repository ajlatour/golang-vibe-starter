package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
)

// TestRun starts the API on a random port against an in-process upstream
// (standing in for the mock container), checks that item requests are
// proxied through it, runs the healthcheck subcommand against it, then
// cancels the context and checks for a clean shutdown.
func TestRun(t *testing.T) {
	upstreamStore := itemtest.NewMemStore()
	seeded, err := upstreamStore.Create(t.Context(), item.Item{Name: "from upstream"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), upstreamStore)
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	env := map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": upstream.URL}
	addr, _, errc := startApp(t, ctx, env)
	base := "http://" + addr

	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get(base + "/healthz")
		if err != nil {
			t.Fatalf("GET /healthz: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("item comes from upstream", func(t *testing.T) {
		resp, err := http.Get(base + "/items/" + seeded.ID)
		if err != nil {
			t.Fatalf("GET item: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if resp.Header.Get(httpx.RequestIDHeader) == "" {
			t.Errorf("response has no %s header", httpx.RequestIDHeader)
		}
		var got item.Item
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != seeded.ID || got.Name != seeded.Name {
			t.Errorf("item = %+v, want %+v", got, seeded)
		}
	})

	t.Run("healthcheck subcommand", func(t *testing.T) {
		probeEnv := map[string]string{"ADDR": addr, "UPSTREAM_URL": upstream.URL}
		if err := run(t.Context(), []string{"healthcheck"}, getenvFrom(probeEnv), io.Discard); err != nil {
			t.Errorf("healthcheck error = %v, want nil", err)
		}
	})

	cancel()
	waitForShutdown(t, errc)
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "missing upstream", env: map[string]string{}},
		{name: "unknown argument", args: []string{"serve"}, env: map[string]string{"UPSTREAM_URL": "http://mock"}},
		{name: "healthcheck with nothing listening", args: []string{"healthcheck"}, env: map[string]string{"ADDR": "127.0.0.1:1", "UPSTREAM_URL": "http://mock"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := run(t.Context(), tt.args, getenvFrom(tt.env), io.Discard); err == nil {
				t.Error("run() error = nil, want error")
			}
		})
	}
}

func TestNewFlags(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	t.Run("embedded flag definitions cover every flag the app declares", func(t *testing.T) {
		if _, err := newFlags(logger, config{}); err != nil {
			t.Fatalf("newFlags() error = %v", err)
		}
	})
	t.Run("definitions file missing a flag is rejected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "flags.json")
		if err := os.WriteFile(path, []byte(`{"flags":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := newFlags(logger, config{FlagsFallbackFile: path})
		if err == nil || !strings.Contains(err.Error(), item.FlagListTotal) {
			t.Errorf("newFlags() error = %v, want one naming %q", err, item.FlagListTotal)
		}
	})
	t.Run("unreadable definitions file is rejected", func(t *testing.T) {
		if _, err := newFlags(logger, config{FlagsFallbackFile: filepath.Join(t.TempDir(), "nope.json")}); err == nil {
			t.Error("newFlags() error = nil, want error")
		}
	})
	t.Run("flagd down falls back to the definitions file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "flags.json")
		if err := os.WriteFile(path, []byte(`{"flags":{"`+item.FlagListTotal+`":{"variants":{"on":true,"off":false},"defaultVariant":"on"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(nettest.DeadURL())

		f, err := newFlags(logger, config{FlagsURL: u, FlagsTimeout: time.Second, FlagsFallbackFile: path})
		if err != nil {
			t.Fatal(err)
		}
		if !f.Bool(t.Context(), item.FlagListTotal) {
			t.Error("Bool() = false, want the definitions-file default (true) when flagd is unreachable")
		}
	})
}
