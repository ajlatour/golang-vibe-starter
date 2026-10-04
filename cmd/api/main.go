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

// version is the build's version, set at link time in container builds
// (-ldflags "-X main.version=..."), where no VCS info is available.
var version string

// buildVersion returns version if it was set at link time, else the version
// recorded in the binary's build info.
func buildVersion() string {
	if version != "" {
		return version
	}
	return httpx.BuildVersion()
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

	logger := cfg.NewLogger(stdout, "api", buildVersion())
	logger.Info("using upstream", "url", cfg.UpstreamURL.Redacted(), "timeout", cfg.UpstreamTimeout.String())
	store := item.NewHTTPStore(cfg.UpstreamURL.String(), &http.Client{Timeout: cfg.UpstreamTimeout})

	evaluator, err := newFlags(logger, cfg)
	if err != nil {
		return err
	}

	return httpx.Serve(ctx, logger, cfg.ServerConfig, newHandler(logger, store, item.WithFlags(evaluator)))
}

// newFlags returns the feature flag evaluator: flagd when FLAGS_URL is set,
// otherwise the flag definitions' default values alone. Either way every flag
// the application declares must be defined there, because those defaults are
// what evaluation falls back to when flagd can't answer.
func newFlags(logger *slog.Logger, cfg config) (item.Flags, error) {
	defaults, err := flags.LoadDefaults(cfg.FlagsFallbackFile)
	if err != nil {
		return nil, err
	}
	if err := defaults.Require(flags.Declared()...); err != nil {
		return nil, err
	}

	if cfg.FlagsURL == nil {
		logger.Info("feature flags: no FLAGS_URL, using default values only")
		return defaults, nil
	}
	logger.Info("using feature flags", "url", cfg.FlagsURL.Redacted(), "timeout", cfg.FlagsTimeout.String())
	return flags.NewOFREP(cfg.FlagsURL.String(), &http.Client{Timeout: cfg.FlagsTimeout}, logger, defaults), nil
}

// newHandler builds the API's root handler: routes plus middleware.
func newHandler(logger *slog.Logger, items item.Store, opts ...item.Option) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	item.RegisterRoutes(mux, logger, items, opts...)
	return httpx.Middleware(logger, mux)
}
