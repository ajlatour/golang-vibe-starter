package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	seeded, err := upstreamStore.Create(t.Context(), itemtest.MustDraft("from upstream", ""))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	mux := http.NewServeMux()
	item.RegisterRoutes(mux, slog.New(slog.DiscardHandler), upstreamStore)
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	env := map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": upstream.URL, "API_AUTH": "off"}
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
		resp, err := http.Get(base + "/items/" + seeded.ID.String())
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
		var got item.Response
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != seeded.ID.String() || got.Name != seeded.Name.String() {
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
		{name: "authentication on, with no keys", env: map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock"}},
		{name: "API_KEYS and API_KEYS_FILE both set", env: map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "API_KEYS": "ci:write:" + strings.Repeat("a", 64), "API_KEYS_FILE": "keys"}},
		{name: "an API_KEYS_FILE that doesn't exist", env: map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "API_KEYS_FILE": "/no/such/keys"}},
		{name: "a bad API_KEYS entry", env: map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "API_KEYS": "ci:write:gvs_the-key-itself"}},
		{name: "pprof address unusable", env: map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "PPROF_ADDR": "no-such-host.invalid:1", "API_AUTH": "off"}},
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

// TestProfiling checks the runtime profiles and metrics are served on
// PPROF_ADDR when it's set, and never on the API's own port, set or not.
func TestProfiling(t *testing.T) {
	get := func(t *testing.T, url string) int {
		t.Helper()
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	t.Run("off by default", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		addr, logs, errc := startApp(t, ctx, map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "API_AUTH": "off"})
		for _, path := range []string{"/debug/pprof/", "/metrics"} {
			if code := get(t, "http://"+addr+path); code != http.StatusNotFound {
				t.Errorf("the API port answers %s with %d, want 404", path, code)
			}
		}
		if strings.Contains(logs.String(), "profiling server started") {
			t.Error("a profiling server started without PPROF_ADDR")
		}
		cancel()
		waitForShutdown(t, errc)
	})

	t.Run("on PPROF_ADDR only", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		addr, logs, errc := startApp(t, ctx, map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "PPROF_ADDR": "127.0.0.1:0", "API_AUTH": "off"})
		var pprofAddr string
		for deadline := time.Now().Add(5 * time.Second); pprofAddr == "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			for _, line := range logs.Lines() {
				var rec struct{ Msg, Addr string }
				if json.Unmarshal([]byte(line), &rec) == nil && rec.Msg == "profiling server started" {
					pprofAddr = rec.Addr
				}
			}
		}
		if pprofAddr == "" {
			t.Fatalf("no profiling server started line; logs: %s", logs)
		}
		if code := get(t, "http://"+pprofAddr+"/debug/pprof/heap?debug=1"); code != http.StatusOK {
			t.Errorf("PPROF_ADDR answers the heap profile with %d, want 200", code)
		}
		get(t, "http://"+addr+"/healthz")
		resp, err := http.Get("http://" + pprofAddr + "/metrics")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("PPROF_ADDR answers /metrics with %d, want 200", resp.StatusCode)
		}
		if want := `http_requests_total{route="GET /healthz",method="GET",status="200"} 1`; !strings.Contains(string(body), want) {
			t.Errorf("/metrics lacks the request just made, %s", want)
		}
		for _, path := range []string{"/debug/pprof/", "/metrics"} {
			if code := get(t, "http://"+addr+path); code != http.StatusNotFound {
				t.Errorf("the API port answers %s with %d, want 404", path, code)
			}
		}
		if code := get(t, "http://"+pprofAddr+"/items"); code != http.StatusNotFound {
			t.Errorf("PPROF_ADDR answers /items with %d, want 404: it serves profiles only", code)
		}
		cancel()
		waitForShutdown(t, errc)
	})
}

// TestNewTransportReusesConnections checks the upstream's connection pool
// keeps a connection for every request that can be in flight: a second burst
// of concurrent calls reuses the first's connections instead of opening new
// ones, as Go's default (2 idle per host) would.
func TestNewTransportReusesConnections(t *testing.T) {
	const concurrent = 20
	var opened atomic.Int32
	release := make(chan struct{})
	var arrived sync.WaitGroup
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Done()
		<-release // hold every request until all are in flight, so each needs its own connection
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: newTransport(concurrent)}
	burst := func() {
		arrived.Add(concurrent)
		var done sync.WaitGroup
		for range concurrent {
			done.Go(func() {
				resp, err := client.Get(srv.URL)
				if err != nil {
					t.Error(err)
					return
				}
				_ = resp.Body.Close()
			})
		}
		arrived.Wait()
		for range concurrent {
			release <- struct{}{}
		}
		done.Wait()
	}
	burst()
	burst()
	if n := opened.Load(); n != concurrent {
		t.Errorf("two bursts of %d calls opened %d connections, want %d: the second should reuse the first's", concurrent, n, concurrent)
	}
}

func TestNewTransport(t *testing.T) {
	for _, tt := range []struct{ maxInFlight, wantIdle int }{{512, 512}, {0, defaultIdleConns}} {
		tr := newTransport(tt.maxInFlight)
		if tr.MaxIdleConnsPerHost != tt.wantIdle || tr.MaxIdleConns != tt.wantIdle {
			t.Errorf("newTransport(%d) keeps %d idle per host, %d in all; want %d", tt.maxInFlight, tr.MaxIdleConnsPerHost, tr.MaxIdleConns, tt.wantIdle)
		}
		if tr.Proxy != nil {
			t.Errorf("newTransport(%d) uses a proxy from the environment; want none", tt.maxInFlight)
		}
	}
}
