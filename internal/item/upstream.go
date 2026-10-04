package item

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
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

// HTTPStatus implements httpx.StatusError. A timeout is the client's 504
// Gateway Timeout; every other failure to get a usable answer from the
// upstream (connection refused, reset, an error status, a body that won't
// decode) is a 502 Bad Gateway. Neither says the API itself failed.
func (e *UpstreamError) HTTPStatus() int {
	var ne net.Error
	if errors.Is(e.Err, context.DeadlineExceeded) || (errors.As(e.Err, &ne) && ne.Timeout()) {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

// LogAttrs implements logx.AttrsError.
func (e *UpstreamError) LogAttrs() []slog.Attr {
	return upstreamAttrs(e.Call, e.Status, e.Duration)
}

var (
	_ logx.AttrsError   = (*UpstreamError)(nil)
	_ httpx.StatusError = (*UpstreamError)(nil)
)

// upstreamAttrs builds the fields describing one upstream call. The status is
// left out when there was no response.
func upstreamAttrs(call string, status int, d time.Duration) []slog.Attr {
	attrs := []slog.Attr{slog.String(logx.KeyUpstream, call)}
	if status != 0 {
		attrs = append(attrs, slog.Int(logx.KeyUpstreamStatus, status))
	}
	return append(attrs, logx.DurationAttr(logx.KeyUpstreamDurationMS, d))
}
