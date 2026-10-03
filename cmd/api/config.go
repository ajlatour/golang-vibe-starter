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
}

func loadConfig(getenv func(string) string) (config, error) {
	srv, err := httpx.LoadServerConfig(getenv)
	cfg := config{ServerConfig: srv, UpstreamTimeout: 5 * time.Second}
	errs := []error{err}

	// Required: there is no sensible default upstream, and silently
	// talking to the wrong one is worse than failing at startup.
	if v := getenv("UPSTREAM_URL"); v == "" {
		errs = append(errs, errors.New("UPSTREAM_URL: required"))
	} else if u, err := url.Parse(v); err != nil {
		errs = append(errs, fmt.Errorf("UPSTREAM_URL: %w", err))
	} else if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("UPSTREAM_URL: must be an absolute http(s) URL, got %q", u.Redacted()))
	} else {
		cfg.UpstreamURL = u
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
