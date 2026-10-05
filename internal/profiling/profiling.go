// Package profiling serves Go's runtime profiles (net/http/pprof) on a
// listener of their own, never on the API's.
//
// Profiles reveal the program's internals: function names, goroutine stacks,
// and in a heap profile, what holds memory. So the endpoints are opt-in (the
// API serves them only when PPROF_ADDR is set), they get their own port that a
// deployment can keep private. Importing net/http/pprof also registers the
// profiles on http.DefaultServeMux, so no server in this module may serve that
// mux; cmd/api's tests check the API doesn't.
package profiling

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// Handler serves the pprof index at /debug/pprof/ and every profile under it:
// profile (CPU), heap, allocs, goroutine, block, mutex, threadcreate, cmdline,
// symbol and trace.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/", pprof.Index) // also serves the named profiles
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return mux
}

// Serve serves Handler on ln until ctx is cancelled. It has no write timeout,
// because a CPU profile or trace streams for as long as the caller asks
// (?seconds=30); a profile still in progress at shutdown is cut off.
//
// The caller listens, so that a bad address fails at startup rather than in
// the background. An error after that is logged, not returned: profiling is a
// diagnostic aid, and losing it mustn't stop the API.
func Serve(ctx context.Context, logger *slog.Logger, ln net.Listener) {
	srv := &http.Server{
		Handler:           Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	logger.Info("profiling server started", logx.KeyAddr, ln.Addr().String())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("profiling server failed", logx.KeyError, err)
	}
}
