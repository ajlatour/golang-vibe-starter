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
		if len(os.Args) == 1 { // the service, not the healthcheck probe
			reportFailure(os.Stdout, os.Getenv, err)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// reportFailure logs why the service stopped, as a log line like every
// other, so a search of the logs finds it: plain text on stderr, alone, would
// be missed by anything reading the JSON lines. It makes its own logger,
// since run's may not exist yet (a configuration error, say).
func reportFailure(w io.Writer, getenv func(string) string, err error) {
	logx.New(w, slog.LevelError, getenv("LOG_FORMAT"), "api", buildVersion()).Error("service failed", logx.KeyError, err)
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

	// Keys from a file are held in a keyring the watcher below can replace.
	if cfg.KeysFile != "" {
		keys, err := apikey.LoadFile(cfg.KeysFile)
		if err != nil {
			return fmt.Errorf("config: API_KEYS_FILE: %w", err)
		}
		cfg.Auth.Ring = apikey.NewKeyring(keys)
	}
	if err := cfg.Auth.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := cfg.NewLogger(stdout, "api", buildVersion())
	profiling.LogRuntime(logger, cfg.RuntimeStatsInterval)
	go profiling.LogRuntimeStats(ctx, logger, cfg.RuntimeStatsInterval)
	logger.Info("using upstream", logx.KeyURL, cfg.UpstreamURL.Redacted(), logx.KeyTimeout, cfg.UpstreamTimeout.String())
	logger.Info("using header guard",
		logx.KeyMode, cfg.HeaderGuard.Mode.String(),
		logx.KeyMaxHeaders, cfg.HeaderGuard.MaxHeaders,
		logx.KeyMaxHeaderBytes, cfg.HeaderGuard.MaxHeaderBytes,
		logx.KeyMaxTotalBytes, cfg.HeaderGuard.MaxTotalBytes)
	logAuth(logger, cfg.Auth)
	if cfg.Auth.Ring != nil {
		go apikey.WatchFile(ctx, logger, cfg.KeysFile, cfg.Auth.Ring, cfg.KeysReload)
	}
	logger.Info("using limits", logx.KeyMaxInFlight, cfg.Limits.MaxInFlight,
		logx.KeyRateLimit, cfg.Limits.Rate, logx.KeyRateBurst, cfg.Limits.Burst)
	store := httpstore.New(cfg.UpstreamURL.String(),
		&http.Client{Timeout: cfg.UpstreamTimeout, Transport: newTransport(cfg.Limits.MaxInFlight)}, httpstore.WithLogger(logger))

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
// the application reads (item.FlagKeys) must be defined there, because those
// defaults are what evaluation falls back to when flagd can't answer.
func newFlags(logger *slog.Logger, cfg config) (item.Flags, error) {
	defaults, err := featureflag.LoadDefaults(cfg.FlagsFallbackFile)
	if err != nil {
		return nil, err
	}
	if err := defaults.Require(item.FlagKeys()...); err != nil {
		return nil, err
	}

	if cfg.FlagsURL == nil {
		logger.Info("feature flags: no FLAGS_URL, using default values only")
		return defaults, nil
	}
	logger.Info("using feature flags", logx.KeyURL, cfg.FlagsURL.Redacted(), logx.KeyTimeout, cfg.FlagsTimeout.String(),
		logx.KeyCacheTTL, cfg.FlagsCacheTTL.String())
	client := &http.Client{Timeout: cfg.FlagsTimeout, Transport: newTransport(cfg.Limits.MaxInFlight)}
	return featureflag.NewOFREP(cfg.FlagsURL.String(), client, logger, defaults, featureflag.WithCache(cfg.FlagsCacheTTL)), nil
}

// defaultIdleConns is how many idle connections newTransport keeps to a
// service when nothing caps the requests in flight.
const defaultIdleConns = 100

// newTransport returns the connection pool for calls to one service (the
// upstream, flagd). Go's default keeps just 2 idle connections per host, so
// under load most calls would open a new one, paying for the handshake and
// leaving sockets in TIME_WAIT; this one keeps as many as there can be
// requests in flight, each of which makes at most one call at a time. It
// also never uses a proxy from HTTP_PROXY and the like: the services are
// named by their own URLs, and a variable set for something else in the
// environment mustn't reroute the calls.
func newTransport(maxInFlight int) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	idle := maxInFlight
	if idle <= 0 {
		idle = defaultIdleConns
	}
	t.MaxIdleConns, t.MaxIdleConnsPerHost = idle, idle
	return t
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
		headerguard.Middleware(logger, cfg.Guard, apikey.Middleware(logger, auth, limit.PerClient(limits, identifyByKey, httpx.RecordRoute(mux))))),
		mwOpts...)
}

// identifyByKey tells the per-client rate limit who a request comes from:
// the API key it was authenticated with, and that key's own limit if it has
// one. A request without a key isn't identified, and is limited by its
// address. Here, where both are wired, is the one place the limiter and
// authentication meet.
func identifyByKey(r *http.Request) (limit.Identity, bool) {
	name := apikey.Name(r.Context())
	if name == "" {
		return limit.Identity{}, false
	}
	rate, burst, _ := apikey.Limits(r.Context())
	return limit.Identity{Client: name, Rate: rate, Burst: burst}, true
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

// logAuth says at startup who may call the API: the keys' names, scopes and
// limits (never the keys) and whether reads need one, or loudly, that nobody
// is checked.
func logAuth(logger *slog.Logger, auth apikey.Policy) {
	if auth.Off {
		logger.Warn("authentication is off: every request is allowed")
		return
	}
	keys := auth.Keys
	if auth.Ring != nil {
		keys = auth.Ring.Keys()
	}
	reads := "required"
	if auth.OpenReads {
		reads = "open"
	}
	logger.Info("using API keys", logx.KeyAPIKeys, apikey.Describe(keys), logx.KeyReads, reads)
}
