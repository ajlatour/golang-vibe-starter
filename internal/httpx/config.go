// Package httpx is the HTTP service plumbing shared by every binary in this
// repo: env config, logging, graceful serving, middleware, JSON helpers, and
// the container healthcheck probe.
package httpx

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// ServerConfig holds the settings every service reads from the environment.
type ServerConfig struct {
	Addr            string
	LogLevel        slog.Level
	LogFormat       string // "json" or "text"
	ShutdownTimeout time.Duration
}

// LoadServerConfig reads ADDR (or PORT), LOG_LEVEL, LOG_FORMAT and
// SHUTDOWN_TIMEOUT, applying defaults. All invalid values are reported
// together.
func LoadServerConfig(getenv func(string) string) (ServerConfig, error) {
	cfg := ServerConfig{
		Addr:            ":8080",
		LogLevel:        slog.LevelInfo,
		LogFormat:       "json",
		ShutdownTimeout: 10 * time.Second,
	}
	var errs []error

	// ADDR takes precedence; PORT is honoured for platforms that set it.
	if v := getenv("ADDR"); v != "" {
		cfg.Addr = v
	} else if v := getenv("PORT"); v != "" {
		cfg.Addr = ":" + v
	}

	if v := getenv("LOG_LEVEL"); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
		}
	}

	if v := getenv("LOG_FORMAT"); v != "" {
		switch v {
		case "json", "text":
			cfg.LogFormat = v
		default:
			errs = append(errs, fmt.Errorf("LOG_FORMAT: must be json or text, got %q", v))
		}
	}

	if v := getenv("SHUTDOWN_TIMEOUT"); v != "" {
		d, err := ParsePositiveDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err))
		} else {
			cfg.ShutdownTimeout = d
		}
	}

	return cfg, errors.Join(errs...)
}

// ParsePositiveDuration parses a time.Duration and rejects zero or negative
// values.
func ParsePositiveDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive, got %s", d)
	}
	return d, nil
}

// NewLogger returns a logger writing to w in the configured format and level.
func (c ServerConfig) NewLogger(w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
