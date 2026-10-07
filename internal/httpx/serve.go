package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// ServeOption changes how Serve runs the server.
type ServeOption func(*serveConfig)

type serveConfig struct {
	srv        *http.Server
	onShutdown []func()
	delay      time.Duration
}

// MaxHeaderBytes caps how much of a request's line and headers the server
// reads before answering 431 itself, without running the handler. Go's
// default is 1 MiB, which every connection could make it hold in memory.
func MaxHeaderBytes(n int) ServeOption {
	return func(c *serveConfig) { c.srv.MaxHeaderBytes = n }
}

// WriteTimeout is how long the server gives a request, from reading its
// headers, to write its response, before cutting the connection: the
// default is 15s. Keep it longer than the request timeout
// (WithRequestTimeout), so a request answers before it, rather than having
// its answer cut off.
func WriteTimeout(d time.Duration) ServeOption {
	return func(c *serveConfig) { c.srv.WriteTimeout = d }
}

// OnShutdown runs f as soon as shutdown begins, before ShutdownDelay and
// before the server stops accepting connections: to start reporting not
// ready, for one.
func OnShutdown(f func()) ServeOption {
	return func(c *serveConfig) { c.onShutdown = append(c.onShutdown, f) }
}

// ShutdownDelay keeps serving for d after shutdown begins, before the server
// stops accepting connections, so a load balancer that has seen the instance
// go unready has time to stop sending it requests.
func ShutdownDelay(d time.Duration) ServeOption {
	return func(c *serveConfig) { c.delay = d }
}

// Serve runs handler on addr until ctx is cancelled, then shuts down
// gracefully, giving in-flight requests up to shutdownTimeout to finish.
// It returns nil after a clean shutdown.
func Serve(ctx context.Context, logger *slog.Logger, addr string, shutdownTimeout time.Duration, handler http.Handler, opts ...ServeOption) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		// The server would answer OPTIONS * itself, with a 200, before the
		// handler and so unlogged; the handler answers it instead (see
		// RejectInvalidTarget).
		DisableGeneralOptionsHandler: true,
	}
	cfg := serveConfig{srv: srv}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Listen separately from Serve so bind errors surface immediately and
	// the real address is known when Addr uses port 0.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	logger.Info("server started", logx.KeyAddr, ln.Addr().String())

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	for _, f := range cfg.onShutdown {
		f()
	}
	logger.Info("shutting down", logx.KeyTimeout, shutdownTimeout.String())
	if cfg.delay > 0 {
		// Still serving: requests keep arriving until load balancers act on
		// the instance being unready.
		select {
		case <-time.After(cfg.delay):
		case err := <-serveErr:
			return fmt.Errorf("serve: %w", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	logger.Info("server stopped")
	return nil
}
