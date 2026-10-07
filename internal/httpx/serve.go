package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// ServeOption changes how Serve runs the server.
type ServeOption func(*serveConfig)

type serveConfig struct {
	srv        *http.Server
	onShutdown []func()
	delay      time.Duration
	rejected   *rejectedLog
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

// LogRejected logs each request the server answers itself, before the
// handler, which so never logs it: one it can't parse (400), one whose
// headers are over MaxHeaderBytes (431), one with a Transfer-Encoding it
// doesn't support (501), and one whose headers don't arrive within the
// read-header timeout, which gets no answer. net/http reports none of these,
// so what's logged, at warn as "request rejected by the server", is that a
// connection closed having read bytes of a request no handler received; the
// status isn't known. A bad request pipelined behind a good one, read before
// the connection went idle, isn't seen. clientIP adds client_ip; count, if not nil, is
// called for each, for a metric.
func LogRejected(clientIP bool, count func()) ServeOption {
	return func(c *serveConfig) { c.rejected = &rejectedLog{clientIP: clientIP, count: count} }
}

type rejectedLog struct {
	clientIP bool
	count    func()
}

// trackedConn counts the bytes read from a connection that belong to no
// request a handler got: those read since the connection was last idle, or
// a handler started on it, unless a handler has finished since (then they
// are its request's unread body, which the server drains or abandons).
type trackedConn struct {
	net.Conn
	unseen  atomic.Int64
	since   atomic.Int64 // UnixNano of the first unseen byte
	handled atomic.Bool  // a handler finished, and the connection hasn't been idle since
}

func (c *trackedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 && c.unseen.Add(int64(n)) == int64(n) {
		c.since.Store(time.Now().UnixNano())
	}
	return n, err
}

// fresh starts the count again: a handler has the request read so far, or
// the connection is idle, waiting for the next.
func (c *trackedConn) fresh() {
	c.unseen.Store(0)
	c.handled.Store(false)
}

// rejected reports whether the connection closed with bytes of a request no
// handler got.
func (c *trackedConn) rejected() bool { return c.unseen.Load() > 0 && !c.handled.Load() }

type trackingListener struct{ net.Listener }

func (l trackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &trackedConn{Conn: c}, nil
}

type trackedConnKey struct{}

// install hooks l into srv and wraps ln: each connection counts what it
// reads, the handler marks it seen as it starts and finishes, and a
// connection that closes with bytes unseen is logged.
func (l *rejectedLog) install(logger *slog.Logger, srv *http.Server, ln net.Listener) net.Listener {
	next := srv.Handler
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := r.Context().Value(trackedConnKey{}).(*trackedConn)
		if ok {
			c.fresh()
			defer c.handled.Store(true)
		}
		next.ServeHTTP(w, r)
	})
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, trackedConnKey{}, c)
	}
	srv.ConnState = func(nc net.Conn, s http.ConnState) {
		c, ok := nc.(*trackedConn)
		if !ok {
			return
		}
		if s == http.StateIdle {
			c.fresh()
			return
		}
		if (s != http.StateClosed && s != http.StateHijacked) || !c.rejected() {
			return
		}
		attrs := []slog.Attr{logx.DurationMS(time.Since(time.Unix(0, c.since.Load())))}
		if l.clientIP {
			attrs = append(attrs, slog.String(logx.KeyClientIP, hostOf(c.RemoteAddr())))
		}
		logger.LogAttrs(context.Background(), slog.LevelWarn, "request rejected by the server", attrs...)
		if l.count != nil {
			l.count()
		}
	}
	return trackingListener{ln}
}

// hostOf is addr's IP address, without the port.
func hostOf(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
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

// ErrShuttingDown is the cause of a request's context ending when shutdown
// ran out of time for it: ServerError answers it with 503, shutting_down.
var ErrShuttingDown = errors.New("the service is shutting down")

// stopGrace is how long requests get to answer once shutdown has stopped
// them, before their connections are closed.
const stopGrace = 2 * time.Second

// Serve runs handler on addr until ctx is cancelled, then shuts down
// gracefully, giving in-flight requests up to shutdownTimeout to finish.
// Those still running then are stopped (their contexts end, with cause
// ErrShuttingDown), so each answers and logs, within stopGrace; only what's
// left after that is cut off, and Serve returns an error saying how many.
// It returns nil after a clean shutdown.
func Serve(ctx context.Context, logger *slog.Logger, addr string, shutdownTimeout time.Duration, handler http.Handler, opts ...ServeOption) error {
	// Every request's context derives from base, so shutdown can stop
	// them all (stopRequests); inFlight counts them, for the log.
	base, stopRequests := context.WithCancelCause(context.WithoutCancel(ctx))
	defer stopRequests(nil)
	var inFlight atomic.Int64
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inFlight.Add(1)
			defer inFlight.Add(-1)
			handler.ServeHTTP(w, r)
		}),
		BaseContext:       func(net.Listener) context.Context { return base },
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
	if cfg.rejected != nil {
		ln = cfg.rejected.install(logger, srv, ln)
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
		// Requests are still in flight after shutdownTimeout. Rather than
		// cut them off unanswered and unlogged, stop them: each one's
		// context ends with ErrShuttingDown, so what it waits on gives up,
		// and it answers 503 (ServerError) and logs it, while its
		// connection is still open. Then close whatever is left.
		logger.LogAttrs(context.Background(), slog.LevelWarn, "shutdown timed out; stopping requests in flight",
			slog.Int64(logx.KeyInFlight, inFlight.Load()))
		stopRequests(ErrShuttingDown)
		graceCtx, cancelGrace := context.WithTimeout(context.WithoutCancel(ctx), stopGrace)
		defer cancelGrace()
		if err := srv.Shutdown(graceCtx); err != nil {
			n := inFlight.Load()
			_ = srv.Close()
			return fmt.Errorf("shutdown: %d requests still in flight %v after being stopped, cut off: %w", n, stopGrace, err)
		}
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	logger.Info("server stopped")
	return nil
}
