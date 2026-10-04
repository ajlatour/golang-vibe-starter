// Package envconfig reads a service's configuration from environment
// variables: a reader that collects every problem so a bad start reports them
// all at once (Env, Optional, Required), and the settings every service
// shares (Service).
package envconfig

import (
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// Service holds the settings every service reads from the environment: where
// to listen, how to log, and how long to wait for in-flight requests on
// shutdown.
type Service struct {
	Addr            string
	LogLevel        slog.Level
	LogFormat       string // "json" or "text"
	ShutdownTimeout time.Duration
}

// LoadService reads ADDR (or PORT), LOG_LEVEL, LOG_FORMAT and
// SHUTDOWN_TIMEOUT, applying defaults. All invalid values are reported
// together.
func LoadService(getenv func(string) string) (Service, error) {
	env := NewEnv(getenv)
	cfg := Service{
		Addr:            serverAddr(env),
		LogLevel:        Optional(env, "LOG_LEVEL", slog.LevelInfo, parseLogLevel),
		LogFormat:       Optional(env, "LOG_FORMAT", "json", parseLogFormat),
		ShutdownTimeout: Optional(env, "SHUTDOWN_TIMEOUT", 10*time.Second, ParsePositiveDuration),
	}
	return cfg, env.Err()
}

// serverAddr returns ADDR, else ":"+PORT for platforms that set only that,
// else ":8080". ADDR takes precedence.
func serverAddr(env *Env) string {
	if v := env.String("ADDR"); v != "" {
		return v
	}
	if v := env.String("PORT"); v != "" {
		return ":" + v
	}
	return ":8080"
}

func parseLogLevel(s string) (slog.Level, error) {
	var l slog.Level
	err := l.UnmarshalText([]byte(s))
	return l, err
}

func parseLogFormat(s string) (string, error) {
	if s != "json" && s != "text" {
		return "", fmt.Errorf("must be json or text, got %q", s)
	}
	return s, nil
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
// Every line carries service and version, and lines logged with a request's
// context carry its request_id.
func (c Service) NewLogger(w io.Writer, service, version string) *slog.Logger {
	return logx.New(w, c.LogLevel, c.LogFormat, service, version)
}
