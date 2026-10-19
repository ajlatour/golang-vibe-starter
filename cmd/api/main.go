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
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
	"github.com/ajlatour/golang-vibe-starter/internal/profiling"
	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"

	// feature:validation
	"github.com/ajlatour/golang-vibe-starter/internal/validator"
	// end feature:validation
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
	if err := loadKeys(&cfg); err != nil {
		return err
	}

	logger := cfg.NewLogger(stdout, "api", buildVersion())
	profiling.LogRuntime(logger, cfg.RuntimeStatsInterval, profiling.LimitMemory(getenv))
	logx.Go(logger, "runtime stats", func() { profiling.LogRuntimeStats(ctx, logger, cfg.RuntimeStatsInterval) })
	logSettings(logger, cfg)
	if cfg.Auth.Ring != nil {
		logx.Go(logger, "API keys reload", func() { apikey.WatchFile(ctx, logger, cfg.KeysFile, cfg.Auth.Ring, cfg.KeysReload) })
	}
	evaluator, err := newFlags(logger, cfg)
	if err != nil {
		return err
	}
	var spans tracectx.Spans
	// feature:tracing
	spans, stopSpans, err := newSpans(ctx, logger, cfg, 5*time.Second)
	if err != nil {
		return err
	}
	defer stopSpans()
	// end feature:tracing
	var (
		allRoutes   []routes
		storeChecks []httpx.Check
	)
	for _, r := range resources {
		rt, checks := r.build(logger, cfg, evaluator)
		allRoutes = append(allRoutes, rt)
		storeChecks = append(storeChecks, checks...)
	}

	// Request metrics: recorded always, served with the runtime's on
	// PPROF_ADDR when it's set.
	metrics := httpmetrics.New()
	served := []func(io.Writer){metrics.WritePrometheus}
	checks := storeChecks
	// feature:validation
	validatorCalls := newValidatorCalls()
	served = append(served, validatorCalls.WritePrometheus)
	// The validation service, if the API can't serve without it.
	v := newValidation(logger, cfg, evaluator)
	checks = append(checks, v.checks...)
	// end feature:validation
	if err := serveProfiling(ctx, logger, cfg, served...); err != nil {
		return err
	}

	// Ready while this instance can reach what it needs (checks), until
	// shutdown.
	readiness := httpx.NewReadiness(logger, time.Second, time.Second, checks...)
	hc := handlerConfig{Guard: cfg.HeaderGuard, Auth: cfg.Auth, Limits: cfg.Limits, Readiness: readiness, Metrics: metrics, ClientIP: cfg.LogClientIP, RequestTimeout: cfg.RequestTimeout, Spans: spans}
	// feature:validation
	hc = v.withValidation(hc, cfg.ValidatorFailOpen, validatorCalls)
	// end feature:validation
	handler := newHandler(logger, hc, allRoutes...)
	return httpx.Serve(ctx, logger, cfg.Addr, cfg.ShutdownTimeout, handler,
		httpx.MaxHeaderBytes(serverHeaderLimit(cfg.HeaderGuard)),
		httpx.WriteTimeout(cfg.RequestTimeout+writeTimeoutMargin),
		httpx.LogRejected(cfg.LogClientIP, metrics.Rejected),
		httpx.OnShutdown(readiness.Stopping), httpx.ShutdownDelay(cfg.ShutdownDelay))
}

// loadKeys completes the authentication settings: keys from API_KEYS_FILE
// go in a keyring the file's watcher can replace, and the result must be
// usable (keys, unless authentication is off).
func loadKeys(cfg *config) error {
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
	return nil
}

// logSettings logs, at startup, the settings in force for the upstream, the
// header guard, authentication and the limits: what a log reader needs to
// know what this instance does.
func logSettings(logger *slog.Logger, cfg config) {
	if cfg.Store == storeMemory {
		logger.Warn("using the in-memory store: for development only; items are lost when the service stops")
	} else {
		logger.Info("using upstream", logx.KeyURL, cfg.UpstreamURL.Redacted(), logx.KeyTimeout, cfg.UpstreamTimeout.String(),
			logx.KeyRetries, cfg.UpstreamRetries)
	}
	logger.Info("using header guard",
		logx.KeyMode, cfg.HeaderGuard.Mode.String(),
		logx.KeyMaxHeaders, cfg.HeaderGuard.MaxHeaders,
		logx.KeyMaxHeaderBytes, cfg.HeaderGuard.MaxHeaderBytes,
		logx.KeyMaxTotalBytes, cfg.HeaderGuard.MaxTotalBytes)
	logAuth(logger, cfg.Auth)
	logger.Info("using limits", logx.KeyMaxInFlight, cfg.Limits.MaxInFlight,
		logx.KeyRateLimit, cfg.Limits.Rate, logx.KeyRateBurst, cfg.Limits.Burst)
}

// resource is one resource the API serves, as main wires it: the feature
// flags its handlers read, which must be defined (newFlags), and build, which
// makes its store, the one STORE chooses, and returns its routes on it and
// the readiness checks of the store. resources lists them all.
type resource struct {
	flagKeys []string
	build    func(logger *slog.Logger, cfg config, f flags) (routes, []httpx.Check)
}

// routes adds a resource's routes to mux.
type routes func(mux *http.ServeMux, logger *slog.Logger)

// flags evaluates boolean feature flags, falling back to their defaults: what
// every resource's handlers, and request validation, read flags with.
type flags interface {
	Bool(ctx context.Context, key string) bool
}

// serveProfiling starts the profiling server, with metrics from extra, when
// PPROF_ADDR is set. It listens here, so a bad address fails at startup.
func serveProfiling(ctx context.Context, logger *slog.Logger, cfg config, extra ...func(io.Writer)) error {
	if cfg.PprofAddr == "" {
		return nil
	}
	ln, err := net.Listen("tcp", cfg.PprofAddr)
	if err != nil {
		return fmt.Errorf("PPROF_ADDR: %w", err)
	}
	logx.Go(logger, "profiling server", func() { profiling.Serve(ctx, logger, ln, extra...) })
	return nil
}

// newFlags returns the feature flag evaluator: flagd when FLAGS_URL is set,
// otherwise the flag definitions' default values alone. Either way every flag
// the application reads (each resource's, and validator.FlagKeys) must be
// defined there, because those defaults are what evaluation falls back to
// when flagd can't answer.
func newFlags(logger *slog.Logger, cfg config) (flags, error) {
	defaults, err := featureflag.LoadDefaults(cfg.FlagsFallbackFile)
	if err != nil {
		return nil, err
	}
	var keys []string
	// feature:validation
	keys = append(keys, validator.FlagKeys()...)
	// end feature:validation
	for _, r := range resources {
		keys = append(keys, r.flagKeys...)
	}
	if err := defaults.Require(keys...); err != nil {
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
	// ClientIP logs each request's client IP (LOG_CLIENT_IP).
	ClientIP bool
	// RequestTimeout is each request's deadline (REQUEST_TIMEOUT). Zero:
	// none.
	RequestTimeout time.Duration
	// Spans records each request's span, and its calls'. Nil
	// records none.
	Spans tracectx.Spans
	// feature:validation
	// Validator checks every request but the probes (internal/validator).
	// Nil checks nothing.
	Validator validator.Checker
	// ValidatorFailOpen lets requests through when it can't answer.
	ValidatorFailOpen bool
	// ValidatorEnforce says whether to act on its answers; nil enforces.
	ValidatorEnforce func(context.Context) bool
	// ValidatorObserve records each check's outcome, for /metrics.
	ValidatorObserve func(validator.Outcome, time.Duration)
	// end feature:validation
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
func newHandler(logger *slog.Logger, cfg handlerConfig, rs ...routes) http.Handler {
	readiness := cfg.Readiness
	if readiness == nil {
		readiness = httpx.NewReadiness(logger, time.Second, time.Second)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+httpx.HealthzPath, httpx.Healthz)
	mux.Handle("GET "+httpx.ReadyzPath, readiness)
	for _, add := range rs {
		add(mux, logger)
	}
	httpx.HandleUnmatched(mux) // JSON 404s and 405s; last, as the catch-all

	probes := []string{httpx.HealthzPath, httpx.ReadyzPath}
	auth, limits := cfg.Auth, cfg.Limits
	auth.Exempt = append(slices.Clip(auth.Exempt), probes...)
	limits.Exempt = append(slices.Clip(limits.Exempt), probes...)
	var mwOpts []httpx.MiddlewareOption
	if cfg.Metrics != nil {
		mwOpts = append(mwOpts, httpx.WithMetrics(cfg.Metrics))
	}
	if cfg.ClientIP {
		mwOpts = append(mwOpts, httpx.WithClientIP())
	}
	if cfg.RequestTimeout > 0 {
		mwOpts = append(mwOpts, httpx.WithRequestTimeout(cfg.RequestTimeout))
	}
	if cfg.Spans != nil {
		mwOpts = append(mwOpts, httpx.WithSpans(cfg.Spans, probes...))
	}
	// The order a request meets them, outermost first. It's a security
	// property, so cmd/api/middleware_order_test.go holds each step to the
	// ones around it.
	return httpx.Chain(httpx.RecordRoute(mux),
		// A correlation ID, the request's log lines, panic recovery and its
		// deadline, around everything, so a request any step refuses is
		// logged like the rest.
		func(h http.Handler) http.Handler { return httpx.Middleware(logger, h, mwOpts...) },
		// A target that isn't a path goes no further.
		httpx.RejectInvalidTarget,
		// The cap on requests in flight: before anything reads the request,
		// so it bounds floods with no valid key too.
		func(h http.Handler) http.Handler { return limit.InFlight(limits, h) },
		// The header guard: before authentication, so a doubled
		// Authorization header is refused before either copy is read.
		func(h http.Handler) http.Handler { return headerguard.Middleware(logger, cfg.Guard, h) },
		// The API version: an unsupported one is refused before the request
		// is read as one.
		func(h http.Handler) http.Handler { return httpx.APIVersion(probes, h) },
		func(h http.Handler) http.Handler { return apikey.Middleware(logger, auth, h) },
		// The per-client rate limit: after authentication, so a client is
		// its key, and a request without a valid one is refused, not counted.
		func(h http.Handler) http.Handler { return limit.PerClient(limits, identifyByKey, h) },
		// feature:validation
		// The request-validation check, if there's one: last, so a client's
		// flood never reaches it, and it knows who's calling.
		func(h http.Handler) http.Handler { return validate(logger, cfg, probes, h) },
		// end feature:validation
	)
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
