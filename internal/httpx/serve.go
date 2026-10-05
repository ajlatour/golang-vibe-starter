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

// ServeOption changes a setting of the server Serve runs.
type ServeOption func(*http.Server)

// MaxHeaderBytes caps how much of a request's line and headers the server
// reads before answering 431 itself, without running the handler. Go's
// default is 1 MiB, which every connection could make it hold in memory.
func MaxHeaderBytes(n int) ServeOption {
	return func(s *http.Server) { s.MaxHeaderBytes = n }
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
	}
	for _, opt := range opts {
		opt(srv)
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

	logger.Info("shutting down", logx.KeyTimeout, shutdownTimeout.String())
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
