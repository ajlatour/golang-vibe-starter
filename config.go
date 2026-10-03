package main

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// config holds settings read from the environment. See README for the list.
type config struct {
	Addr            string
	LogLevel        slog.Level
	LogFormat       string // "json" or "text"
	ShutdownTimeout time.Duration
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
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
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err))
		case d <= 0:
			errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT: must be positive, got %s", d))
		default:
			cfg.ShutdownTimeout = d
		}
	}

	return cfg, errors.Join(errs...)
}
