package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	// feature:tracing
	"strings"
	// end feature:tracing
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/envconfig"
	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
)

// config is the API's environment configuration: the shared server settings
// plus where to reach the upstream items service.
type config struct {
	envconfig.Service
	// RequestTimeout is how long a request has, from arriving, to be
	// answered (httpx.WithRequestTimeout). The server's write timeout is
	// longer by writeTimeoutMargin, so the answer always gets out.
	RequestTimeout time.Duration

	// Store is where items are kept: storeUpstream (the default), the
	// upstream service at UpstreamURL; or storeMemory, the process's memory,
	// for development and tests (STORE).
	Store string

	UpstreamURL     *url.URL
	UpstreamTimeout time.Duration
	// UpstreamRetries is how often a GET to the upstream is tried again
	// after a fast failure (httpstore.WithRetries).
	UpstreamRetries int

	// FlagsURL is the OFREP endpoint of the flagd sidecar. Nil disables
	// feature flags: every flag then takes its default.
	FlagsURL     *url.URL
	FlagsTimeout time.Duration
	// FlagsCacheTTL is how long a flag's value is kept after flagd serves
	// it, and how often flagd is retried during an outage. Zero: neither.
	FlagsCacheTTL time.Duration

	// FlagsFallbackFile is the path of a flagd-format flag definitions file
	// whose default variants replace those of the copy compiled into the
	// binary. Empty uses the embedded copy.
	FlagsFallbackFile string

	// PprofAddr is where to serve the runtime profiles (internal/profiling).
	// Empty, the default, serves none.
	PprofAddr string

	// RuntimeStatsInterval is how often to log the heap, memory and
	// goroutines ("runtime stats"); zero logs none.
	RuntimeStatsInterval time.Duration

	// LogClientIP adds client_ip to each request's log lines.
	LogClientIP bool

	// feature:tracing
	// TracesEndpoint is the URL spans are exported to over OTLP/HTTP:
	// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, or OTEL_EXPORTER_OTLP_ENDPOINT with
	// /v1/traces added, as OpenTelemetry specifies. Nil, the default, records
	// none (telemetry.go).
	TracesEndpoint *url.URL
	// end feature:tracing

	// ValidatorURL is the request-validation service every request is
	// checked against (internal/validator). Nil: no check.
	ValidatorURL     *url.URL
	ValidatorTimeout time.Duration
	// ValidatorFailOpen lets requests through when the service can't
	// answer; the default refuses them (VALIDATOR_FAILURE_MODE).
	ValidatorFailOpen bool

	// HeaderGuard is the header guard's policy: headerguard.DefaultPolicy,
	// with the mode and limits overridden by the environment.
	HeaderGuard headerguard.Policy

	// Auth is who may call the API: the keys in API_KEYS, whether reads
	// need one (API_AUTH_READS), or nobody checked at all (API_AUTH=off).
	// run refuses to start with it on and no keys.
	Auth apikey.Policy

	// Limits caps requests in flight, and each client's rate.
	Limits limit.Policy

	// KeysFile is API_KEYS_FILE: keys to read from a file, and reload every
	// KeysReload while the service runs, instead of API_KEYS. Empty: none.
	KeysFile   string
	KeysReload time.Duration

	// ShutdownDelay is how long the service keeps serving after a shutdown
	// signal, reporting not ready, before it stops accepting connections:
	// time for a load balancer to stop sending it requests.
	ShutdownDelay time.Duration
}

// Where items can be kept (STORE).
const (
	storeUpstream = "upstream"
	storeMemory   = "memory"
)

func loadConfig(getenv func(string) string) (config, error) {
	srv, srvErr := envconfig.LoadService(getenv)
	env := envconfig.NewEnv(getenv)
	store := envconfig.Optional(env, "STORE", storeUpstream, parseStore)
	// Required with the upstream store: there is no sensible default
	// upstream, and silently talking to the wrong one is worse than failing
	// at startup. Refused with the memory store, which would ignore it.
	var upstreamURL *url.URL
	switch {
	case store == storeUpstream:
		upstreamURL = envconfig.Required(env, "UPSTREAM_URL", parseHTTPURL)
	case env.String("UPSTREAM_URL") != "":
		upstreamURL = envconfig.Optional(env, "UPSTREAM_URL", nil, func(string) (*url.URL, error) {
			return nil, errors.New("set with STORE=memory, which doesn't use it: unset it, or use STORE=upstream")
		})
	}
	cfg := config{
		Service: srv,

		// Ten seconds: twice the upstream's own timeout, for a request that
		// makes two upstream calls (a conditional write reads, then writes),
		// and the shutdown timeout's default, so a request in flight at
		// shutdown can finish.
		RequestTimeout: envconfig.Optional(env, "REQUEST_TIMEOUT", 10*time.Second, envconfig.ParsePositiveDuration),

		Store:           store,
		UpstreamURL:     upstreamURL,
		UpstreamTimeout: envconfig.Optional(env, "UPSTREAM_TIMEOUT", 5*time.Second, envconfig.ParsePositiveDuration),
		// One retry: enough to ride out a dropped connection or a restart
		// behind a load balancer, and the budget keeps it from adding load
		// when the upstream is really down.
		UpstreamRetries: envconfig.Optional(env, "UPSTREAM_RETRIES", 1, parseRetries),

		// Optional: flags are an add-on, and without a sidecar the service
		// runs with every flag at its default. The timeout is short by
		// default because flag evaluation sits on the request path.
		FlagsURL:     envconfig.Optional(env, "FLAGS_URL", nil, parseHTTPURL),
		FlagsTimeout: envconfig.Optional(env, "FLAGS_TIMEOUT", 500*time.Millisecond, envconfig.ParsePositiveDuration),
		// A second: flag changes show within one, and flagd is asked about
		// each flag about once a second however busy the service is.
		FlagsCacheTTL:     envconfig.Optional(env, "FLAGS_CACHE_TTL", time.Second, parseNonNegativeDuration),
		FlagsFallbackFile: env.String("FLAGS_FALLBACK_FILE"),

		// Off unless asked for: profiles expose the program's internals.
		PprofAddr: env.String("PPROF_ADDR"),

		// On by default: where the logs are all there is, these lines are
		// the only view of memory over time, and one every 5 minutes costs
		// nothing worth counting.
		RuntimeStatsInterval: envconfig.Optional(env, "RUNTIME_STATS_INTERVAL", 5*time.Minute, parseNonNegativeDuration),

		// On by default: which client sent a request is a first question
		// when debugging. An IP address is personal data in many
		// jurisdictions, so a deployment can turn it off.
		LogClientIP: envconfig.Optional(env, "LOG_CLIENT_IP", true, parseOnOff),

		// feature:tracing
		// Off unless there's somewhere to send spans. The OpenTelemetry SDK
		// reads the rest of its settings itself (internal/telemetry).
		TracesEndpoint: envconfig.Optional(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
			envconfig.Optional(env, "OTEL_EXPORTER_OTLP_ENDPOINT", nil, parseOTLPEndpoint), parseHTTPURL),
		// end feature:tracing

		// Optional, so local development and tests can run without one, but
		// loud when unset (see run). The timeout is short because the check
		// is on every request's path, and failing closed is the default
		// because a check the API can't serve without shouldn't be skipped
		// quietly.
		ValidatorURL:      envconfig.Optional(env, "VALIDATOR_URL", nil, parseHTTPURL),
		ValidatorTimeout:  envconfig.Optional(env, "VALIDATOR_TIMEOUT", 300*time.Millisecond, envconfig.ParsePositiveDuration),
		ValidatorFailOpen: envconfig.Optional(env, "VALIDATOR_FAILURE_MODE", false, parseFailureMode),
	}

	// The header guard: the defaults live in headerguard.DefaultPolicy, and
	// only the mode and the three limits can be changed here. Which headers
	// are single-use or stripped stays in code.
	guard := headerguard.DefaultPolicy()
	guard.Mode = envconfig.Optional(env, "HEADER_GUARD_MODE", guard.Mode, headerguard.ParseMode)
	guard.MaxHeaders = envconfig.Optional(env, "HEADER_MAX_COUNT", guard.MaxHeaders, headerguard.ParseLimit)
	guard.MaxHeaderBytes = envconfig.Optional(env, "HEADER_MAX_BYTES", guard.MaxHeaderBytes, headerguard.ParseLimit)
	guard.MaxTotalBytes = envconfig.Optional(env, "HEADER_MAX_TOTAL_BYTES", guard.MaxTotalBytes, headerguard.ParseLimit)
	cfg.HeaderGuard = guard

	// Authentication: on, with every request needing a key, unless told
	// otherwise. (newHandler exempts the probes, /healthz and /readyz.)
	cfg.Auth = apikey.Policy{
		Off:       envconfig.Optional(env, "API_AUTH", false, parseAuthOff),
		OpenReads: envconfig.Optional(env, "API_AUTH_READS", false, parseOpenReads),
		Keys:      envconfig.Optional(env, "API_KEYS", nil, apikey.ParseKeys),
	}

	// Limits: on by default, generous enough that a well-behaved client never
	// meets them. (newHandler exempts the probes, so a busy service isn't
	// also restarted, or taken out of service, by its own probes.)
	cfg.Limits = limit.Policy{
		MaxInFlight: envconfig.Optional(env, "MAX_IN_FLIGHT", 512, strconv.Atoi),
		Rate:        envconfig.Optional(env, "RATE_LIMIT", 100.0, parseRate),
		Burst:       envconfig.Optional(env, "RATE_BURST", 200, strconv.Atoi),
	}

	cfg.ShutdownDelay = envconfig.Optional(env, "SHUTDOWN_DELAY", 0, parseNonNegativeDuration)
	cfg.KeysFile = env.String("API_KEYS_FILE")
	cfg.KeysReload = envconfig.Optional(env, "API_KEYS_RELOAD", 10*time.Second, envconfig.ParsePositiveDuration)

	errs := []error{srvErr, env.Err()}
	if cfg.KeysFile != "" && len(cfg.Auth.Keys) > 0 {
		errs = append(errs, errors.New("API_KEYS and API_KEYS_FILE: set one, not both"))
	}
	if err := cfg.Limits.Validate(); err != nil && env.Err() == nil {
		errs = append(errs, err)
	}
	if err := guard.Validate(); err != nil && env.Err() == nil {
		errs = append(errs, fmt.Errorf("HEADER_MAX_BYTES, HEADER_MAX_TOTAL_BYTES: %w", err))
	}
	if srvErr == nil && env.Err() == nil {
		errs = append(errs, cfg.checkTimeouts()...)
	}
	return cfg, errors.Join(errs...)
}

// checkTimeouts refuses timeouts that can't work together. Each call a
// request makes must be able to time out on its own before the request's
// deadline cuts it short, or its timeout would never be the one that fires;
// and shutdown must give a request in flight the whole of its time, or it's
// cut off unanswered.
func (cfg config) checkTimeouts() []error {
	var errs []error
	for _, t := range []struct {
		name string
		d    time.Duration
		set  bool
	}{
		{"UPSTREAM_TIMEOUT", cfg.UpstreamTimeout, cfg.Store == storeUpstream},
		{"VALIDATOR_TIMEOUT", cfg.ValidatorTimeout, cfg.ValidatorURL != nil},
		{"FLAGS_TIMEOUT", cfg.FlagsTimeout, cfg.FlagsURL != nil},
	} {
		if t.set && t.d >= cfg.RequestTimeout {
			errs = append(errs, fmt.Errorf("%s (%v) must be shorter than REQUEST_TIMEOUT (%v), the time a whole request has", t.name, t.d, cfg.RequestTimeout))
		}
	}
	if cfg.ShutdownTimeout < cfg.RequestTimeout {
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT (%v) must be at least REQUEST_TIMEOUT (%v), so a request in flight at shutdown can finish", cfg.ShutdownTimeout, cfg.RequestTimeout))
	}
	return errs
}

// writeTimeoutMargin is how much longer than REQUEST_TIMEOUT the server
// gives a response to be written: time for the handler to answer once the
// deadline passes, and for the answer to reach the client.
const writeTimeoutMargin = 5 * time.Second

// parseStore reads STORE: upstream or memory.
func parseStore(v string) (string, error) {
	if v != storeUpstream && v != storeMemory {
		return "", fmt.Errorf("want upstream or memory, got %q", v)
	}
	return v, nil
}

// parseHTTPURL parses v as an absolute http(s) base URL, without a query
// string or fragment.
func parseHTTPURL(v string) (*url.URL, error) {
	u, err := url.Parse(v)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("must be an absolute http(s) URL, got %q", u.Redacted())
	}
	// These are base URLs that request paths are appended to, so a query or
	// fragment could never work, and any token in one would end up in logged
	// errors.
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("must not contain a query string or fragment, got %q", u.Redacted())
	}
	return u, nil
}

// parseAuthOff reads API_AUTH: on (the default) or off.
func parseAuthOff(v string) (bool, error) {
	switch v {
	case "on":
		return false, nil
	case "off":
		return true, nil
	}
	return false, fmt.Errorf("want on or off, got %q", v)
}

// parseOpenReads reads API_AUTH_READS: required (the default) or open.
func parseOpenReads(v string) (bool, error) {
	switch v {
	case "required":
		return false, nil
	case "open":
		return true, nil
	}
	return false, fmt.Errorf("want required or open, got %q", v)
}

// parseRate reads RATE_LIMIT: requests per second, 0 for no limit.
func parseRate(v string) (float64, error) {
	return strconv.ParseFloat(v, 64)
}

// parseNonNegativeDuration reads a duration that may be zero.
func parseNonNegativeDuration(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err == nil && d < 0 {
		err = fmt.Errorf("must not be negative, got %s", v)
	}
	return d, err
}

// parseOnOff reads an on/off setting.
func parseOnOff(v string) (bool, error) {
	switch v {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("want on or off, got %q", v)
}

// parseFailureMode reads VALIDATOR_FAILURE_MODE: closed (the default)
// refuses requests when the validation service can't answer, open lets them
// through.
func parseFailureMode(v string) (bool, error) {
	switch v {
	case "closed":
		return false, nil
	case "open":
		return true, nil
	}
	return false, fmt.Errorf("want closed or open, got %q", v)
}

// parseRetries reads UPSTREAM_RETRIES: 0 to 3.
func parseRetries(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 3 {
		return 0, fmt.Errorf("want 0 to 3, got %q", v)
	}
	return n, nil
}

// feature:tracing
// parseOTLPEndpoint reads OTEL_EXPORTER_OTLP_ENDPOINT, a base URL, as the
// URL traces go to: its path with /v1/traces added.
func parseOTLPEndpoint(s string) (*url.URL, error) {
	u, err := parseHTTPURL(s)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/traces"
	return u, nil
}

// end feature:tracing
