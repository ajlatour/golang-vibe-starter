// Command api runs the public HTTP API. Item data comes from an upstream
// service at UPSTREAM_URL (the mock service in local development).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/featureflag"
	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpmetrics"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
	"github.com/ajlatour/golang-vibe-starter/internal/profiling"
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

	if err := cfg.Auth.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := cfg.NewLogger(stdout, "api", buildVersion())
	logger.Info("using upstream", logx.KeyURL, cfg.UpstreamURL.Redacted(), logx.KeyTimeout, cfg.UpstreamTimeout.String())
	logger.Info("using header guard",
		logx.KeyMode, cfg.HeaderGuard.Mode.String(),
		logx.KeyMaxHeaders, cfg.HeaderGuard.MaxHeaders,
		logx.KeyMaxHeaderBytes, cfg.HeaderGuard.MaxHeaderBytes,
		logx.KeyMaxTotalBytes, cfg.HeaderGuard.MaxTotalBytes)
	logAuth(logger, cfg.Auth)
	logger.Info("using limits", logx.KeyMaxInFlight, cfg.Limits.MaxInFlight,
		logx.KeyRateLimit, cfg.Limits.Rate, logx.KeyRateBurst, cfg.Limits.Burst)
	store := httpstore.New(cfg.UpstreamURL.String(), &http.Client{Timeout: cfg.UpstreamTimeout}, httpstore.WithLogger(logger))

	evaluator, err := newFlags(logger, cfg)
	if err != nil {
		return err
	}

	// Request metrics: recorded always, served with the runtime's on
	// PPROF_ADDR when it's set.
	metrics := httpmetrics.New()
	if cfg.PprofAddr != "" {
		ln, err := net.Listen("tcp", cfg.PprofAddr)
		if err != nil {
			return fmt.Errorf("PPROF_ADDR: %w", err)
		}
		go profiling.Serve(ctx, logger, ln, metrics.WritePrometheus)
	}

	// Ready while this instance can reach the upstream, until shutdown.
	readiness := httpx.NewReadiness(logger, time.Second, time.Second, httpx.Check{Name: "upstream", Fn: store.Reachable})
	handler := newHandler(logger, handlerConfig{Guard: cfg.HeaderGuard, Auth: cfg.Auth, Limits: cfg.Limits, Readiness: readiness, Metrics: metrics},
		store, item.WithFlags(evaluator))
	return httpx.Serve(ctx, logger, cfg.Addr, cfg.ShutdownTimeout, handler,
		httpx.MaxHeaderBytes(serverHeaderLimit(cfg.HeaderGuard)),
		httpx.OnShutdown(readiness.Stopping), httpx.ShutdownDelay(cfg.ShutdownDelay))
}

// newFlags returns the feature flag evaluator: flagd when FLAGS_URL is set,
// otherwise the flag definitions' default values alone. Either way every flag
// the application declares must be defined there, because those defaults are
// what evaluation falls back to when flagd can't answer.
func newFlags(logger *slog.Logger, cfg config) (item.Flags, error) {
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

// handlerConfig is everything newHandler puts in front of the routes.
type handlerConfig struct {
	Guard  headerguard.Policy
	Auth   apikey.Policy
	Limits limit.Policy
	// Readiness answers /readyz. Nil reports ready, always.
	Readiness *httpx.Readiness
	// Metrics records every request, for /metrics. Nil records nothing.
	Metrics *httpmetrics.Recorder
}

// newHandler builds the API's root handler. From the outside in: the standard
// middleware (correlation ID, logging), the cap on requests in flight, the
// header guard, authentication, the per-client rate limit, and the routes.
// The cap comes first so it bounds floods of requests with no valid key too;
// the guard runs before authentication, so a doubled Authorization header is
// refused before either copy is read; the rate limit comes after it, so a
// client is its key. All sit inside the middleware, so a request they refuse
// still gets a correlation ID and its log lines. The probes, /healthz and
// /readyz, are exempt from authentication and the limits.
func newHandler(logger *slog.Logger, cfg handlerConfig, items item.Store, opts ...item.Option) http.Handler {
	readiness := cfg.Readiness
	if readiness == nil {
		readiness = httpx.NewReadiness(logger, time.Second, time.Second)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+httpx.HealthzPath, httpx.Healthz)
	mux.Handle("GET "+httpx.ReadyzPath, readiness)
	item.RegisterRoutes(mux, logger, items, opts...)

	probes := []string{httpx.HealthzPath, httpx.ReadyzPath}
	auth, limits := cfg.Auth, cfg.Limits
	auth.Exempt = append(slices.Clip(auth.Exempt), probes...)
	limits.Exempt = append(slices.Clip(limits.Exempt), probes...)
	var mwOpts []httpx.MiddlewareOption
	if cfg.Metrics != nil {
		mwOpts = append(mwOpts, httpx.WithMetrics(cfg.Metrics))
	}
	return httpx.Middleware(logger, limit.InFlight(limits,
		headerguard.Middleware(logger, cfg.Guard, apikey.Middleware(logger, auth, limit.PerClient(limits, httpx.RecordRoute(mux))))),
		mwOpts...)
}

// serverHeaderLimit is how much of a request's line and headers Go's server
// reads before refusing the request itself: twice the header guard's total.
// The guard measures headers alone, and a request line can be long, so the
// slack leaves it to answer most oversized requests with its JSON 431; only
// a request far over the limit gets Go's plain one. The point is the cap:
// Go's default of 1 MiB is far more than the guard ever lets through.
func serverHeaderLimit(guard headerguard.Policy) int {
	return 2 * guard.MaxTotalBytes
}

// logAuth says at startup who may call the API: the keys' names (never the
// keys) and whether reads need one, or loudly, that nobody is checked.
func logAuth(logger *slog.Logger, auth apikey.Policy) {
	if auth.Off {
		logger.Warn("authentication is off: every request is allowed")
		return
	}
	names := make([]string, len(auth.Keys))
	for i, k := range auth.Keys {
		names[i] = k.Name + " (" + k.Scope.String() + ")"
	}
	reads := "required"
	if auth.OpenReads {
		reads = "open"
	}
	logger.Info("using API keys", logx.KeyAPIKeys, strings.Join(names, ", "), logx.KeyReads, reads)
}
