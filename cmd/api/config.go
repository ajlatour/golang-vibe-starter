package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
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
	UpstreamURL     *url.URL
	UpstreamTimeout time.Duration

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

func loadConfig(getenv func(string) string) (config, error) {
	srv, srvErr := envconfig.LoadService(getenv)
	env := envconfig.NewEnv(getenv)
	cfg := config{
		Service: srv,

		// Required: there is no sensible default upstream, and silently
		// talking to the wrong one is worse than failing at startup.
		UpstreamURL:     envconfig.Required(env, "UPSTREAM_URL", parseHTTPURL),
		UpstreamTimeout: envconfig.Optional(env, "UPSTREAM_TIMEOUT", 5*time.Second, envconfig.ParsePositiveDuration),

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
	return cfg, errors.Join(errs...)
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
