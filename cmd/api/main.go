// Command api runs the public HTTP API. Item data comes from an upstream
// service at UPSTREAM_URL (the mock service in local development).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ajlatour/golang-vibe-starter/internal/flags"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, restore default handling so a second one kills
	// the process immediately instead of waiting for graceful shutdown.
	go func() {
		<-ctx.Done()
		stop()
	}()

	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// run wires up and runs the service until ctx is cancelled. With the single
// argument "healthcheck" it instead probes an already-running instance, for
// use as a container healthcheck.
func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "healthcheck":
		return httpx.Probe(ctx, cfg.Addr)
	default:
		return fmt.Errorf("unexpected arguments %q (want none, or \"healthcheck\")", args)
	}

	logger := cfg.NewLogger(stdout)
	logger.Info("using upstream", "url", cfg.UpstreamURL.Redacted(), "timeout", cfg.UpstreamTimeout)
	store := item.NewHTTPStore(cfg.UpstreamURL.String(), &http.Client{Timeout: cfg.UpstreamTimeout})

	var opts []item.Option
	if cfg.FlagsURL != nil {
		logger.Info("using feature flags", "url", cfg.FlagsURL.Redacted(), "timeout", cfg.FlagsTimeout)
		opts = append(opts, item.WithFlags(flags.NewOFREP(cfg.FlagsURL.String(), &http.Client{Timeout: cfg.FlagsTimeout}, logger)))
	}

	return httpx.Serve(ctx, logger, cfg.ServerConfig, newHandler(logger, store, opts...))
}

// newHandler builds the API's root handler: routes plus middleware.
func newHandler(logger *slog.Logger, items item.Store, opts ...item.Option) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	item.RegisterRoutes(mux, logger, items, opts...)
	return httpx.Middleware(logger, mux)
}
