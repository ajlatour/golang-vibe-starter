package main

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// config is the API's environment configuration: the shared server settings
// plus where to reach the upstream items service.
type config struct {
	httpx.ServerConfig
	UpstreamURL     *url.URL
	UpstreamTimeout time.Duration

	// FlagsURL is the OFREP endpoint of the flagd sidecar. Nil disables
	// feature flags: every flag then takes its default.
	FlagsURL     *url.URL
	FlagsTimeout time.Duration
}

func loadConfig(getenv func(string) string) (config, error) {
	srv, err := httpx.LoadServerConfig(getenv)
	cfg := config{ServerConfig: srv, UpstreamTimeout: 5 * time.Second, FlagsTimeout: 500 * time.Millisecond}
	errs := []error{err}

	// Required: there is no sensible default upstream, and silently
	// talking to the wrong one is worse than failing at startup.
	if v := getenv("UPSTREAM_URL"); v == "" {
		errs = append(errs, errors.New("UPSTREAM_URL: required"))
	} else if u, err := parseHTTPURL(v); err != nil {
		errs = append(errs, fmt.Errorf("UPSTREAM_URL: %w", err))
	} else {
		cfg.UpstreamURL = u
	}

	// Optional: flags are an add-on, and without a sidecar the service
	// runs with every flag at its default.
	if v := getenv("FLAGS_URL"); v != "" {
		if u, err := parseHTTPURL(v); err != nil {
			errs = append(errs, fmt.Errorf("FLAGS_URL: %w", err))
		} else {
			cfg.FlagsURL = u
		}
	}

	// Short by default: flag evaluation sits on the request path.
	if v := getenv("FLAGS_TIMEOUT"); v != "" {
		d, err := httpx.ParsePositiveDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("FLAGS_TIMEOUT: %w", err))
		} else {
			cfg.FlagsTimeout = d
		}
	}

	if v := getenv("UPSTREAM_TIMEOUT"); v != "" {
		d, err := httpx.ParsePositiveDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("UPSTREAM_TIMEOUT: %w", err))
		} else {
			cfg.UpstreamTimeout = d
		}
	}

	return cfg, errors.Join(errs...)
}

// parseHTTPURL parses v as an absolute http(s) URL.
func parseHTTPURL(v string) (*url.URL, error) {
	u, err := url.Parse(v)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("must be an absolute http(s) URL, got %q", u.Redacted())
	}
	return u, nil
}
