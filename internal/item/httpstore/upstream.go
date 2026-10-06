package httpstore

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// UpstreamError is a failed call to the upstream: a network error, timeout,
// unexpected status, or unreadable response. It keeps the original error
// message and chain (errors.Is and errors.As see through it) and adds what
// ServerError logs to make the failure debuggable from one line.
type UpstreamError struct {
	Call     string        // "METHOD /path"
	Status   int           // response status; 0 if there was no response
	Duration time.Duration // time spent on the call, including reading the response
	Err      error
}

func (e *UpstreamError) Error() string { return e.Err.Error() }
func (e *UpstreamError) Unwrap() error { return e.Err }

// Is says what kind of store failure e is, for errors.Is: a timeout is
// item.ErrStoreTimeout, and every other failure to get a usable answer from
// the upstream (connection refused, reset, an error status, a body that won't
// decode) item.ErrStoreUnavailable. What the client is told is the item
// handlers' to decide, not this store's.
func (e *UpstreamError) Is(target error) bool {
	switch target {
	case item.ErrStoreTimeout:
		return e.timeout()
	case item.ErrStoreUnavailable:
		return !e.timeout()
	}
	return false
}

func (e *UpstreamError) timeout() bool {
	var ne net.Error
	return errors.Is(e.Err, context.DeadlineExceeded) || (errors.As(e.Err, &ne) && ne.Timeout())
}

// LogAttrs implements logx.AttrsError.
func (e *UpstreamError) LogAttrs() []slog.Attr {
	return upstreamAttrs(e.Call, e.Status, e.Duration)
}

var _ logx.AttrsError = (*UpstreamError)(nil)

// upstreamAttrs builds the fields describing one upstream call. The status is
// left out when there was no response.
func upstreamAttrs(call string, status int, d time.Duration) []slog.Attr {
	attrs := []slog.Attr{slog.String(logx.KeyUpstream, call)}
	if status != 0 {
		attrs = append(attrs, slog.Int(logx.KeyUpstreamStatus, status))
	}
	return append(attrs, logx.DurationAttr(logx.KeyUpstreamDurationMS, d))
}
