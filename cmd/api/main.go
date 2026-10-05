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

	"github.com/ajlatour/golang-vibe-starter/internal/featureflag"
	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpapi"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
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
	return logx.BuildVersion()
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
	logger.Info("using upstream", logx.KeyURL, cfg.UpstreamURL.Redacted(), logx.KeyTimeout, cfg.UpstreamTimeout.String())
	logger.Info("using header guard",
		logx.KeyMode, cfg.HeaderGuard.Mode.String(),
		logx.KeyMaxHeaders, cfg.HeaderGuard.MaxHeaders,
		logx.KeyMaxHeaderBytes, cfg.HeaderGuard.MaxHeaderBytes,
		logx.KeyMaxTotalBytes, cfg.HeaderGuard.MaxTotalBytes)
	store := httpstore.New(cfg.UpstreamURL.String(), &http.Client{Timeout: cfg.UpstreamTimeout}, httpstore.WithLogger(logger))

	evaluator, err := newFlags(logger, cfg)
	if err != nil {
		return err
	}

	return httpx.Serve(ctx, logger, cfg.Addr, cfg.ShutdownTimeout, newHandler(logger, cfg.HeaderGuard, store, httpapi.WithFlags(evaluator)))
}

// newFlags returns the feature flag evaluator: flagd when FLAGS_URL is set,
// otherwise the flag definitions' default values alone. Either way every flag
// the application declares must be defined there, because those defaults are
// what evaluation falls back to when flagd can't answer.
func newFlags(logger *slog.Logger, cfg config) (httpapi.Flags, error) {
	defaults, err := featureflag.LoadDefaults(cfg.FlagsFallbackFile)
	if err != nil {
		return nil, err
	}
	if err := defaults.Require(featureflag.Declared()...); err != nil {
		return nil, err
	}

	if cfg.FlagsURL == nil {
		logger.Info("feature flags: no FLAGS_URL, using default values only")
		return defaults, nil
	}
	logger.Info("using feature flags", logx.KeyURL, cfg.FlagsURL.Redacted(), logx.KeyTimeout, cfg.FlagsTimeout.String())
	return featureflag.NewOFREP(cfg.FlagsURL.String(), &http.Client{Timeout: cfg.FlagsTimeout}, logger, defaults), nil
}

// newHandler builds the API's root handler: routes, the header guard, and
// the standard middleware around them. The guard sits inside the middleware,
// so a request it rejects still gets a correlation ID and its log lines.
func newHandler(logger *slog.Logger, guard headerguard.Policy, items item.Store, opts ...httpapi.Option) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+httpx.HealthzPath, httpx.Healthz)
	httpapi.RegisterRoutes(mux, logger, items, opts...)

	return httpx.Middleware(logger, headerguard.Middleware(logger, guard, mux))
}
