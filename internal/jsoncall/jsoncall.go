// Package jsoncall makes one JSON-over-HTTP call to another service and
// reports failures in a consistent, log-safe way. It holds the steps every
// outbound client here repeats: encode the request, send it, redact the URL in
// transport errors, refuse redirects, cap and drain the response, check the
// status, and decode the body. Each client supplies only what differs: how to describe itself in
// errors, what an error body looks like, and any status with special meaning.
package jsoncall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// DefaultMaxResponseBytes caps how much of a response is read when Call
// doesn't set its own cap.
const DefaultMaxResponseBytes = 1 << 20 // 1 MiB

// Call describes one request. The zero value of an optional field means "not
// used".
type Call struct {
	Client *http.Client
	Method string
	URL    string

	// Label starts every error message, naming the call for whoever reads the
	// log, e.g. "upstream GET /items". It must already be safe to log: bounded
	// in length and free of credentials.
	Label string

	In  any // request body, sent as JSON; nil for none
	Out any // where to decode a successful response; nil to ignore the body

	// Want is the success status. Any other status is an error.
	Want int

	// MaxResponseBytes caps how much of the response body is read; zero means
	// DefaultMaxResponseBytes.
	MaxResponseBytes int64

	// OnStatus, if set, sees a response whose status isn't Want before it is
	// turned into a generic error. If it returns an error, Do returns that
	// error as is (not wrapped in *Error, not labelled), so a client can give
	// a status its own meaning, such as a sentinel "not found".
	OnStatus func(resp *http.Response) error

	// ErrorDetail, if set, reads an unexpected response's body and returns a
	// short, already-bounded description to append to the error message, after
	// "unexpected status N: ". The body is not otherwise logged.
	ErrorDetail func(body io.Reader) string
}

// Kind says how a call failed.
type Kind int

const (
	// KindRequest: the request couldn't be built or encoded; nothing was sent.
	KindRequest Kind = iota + 1
	// KindTransport: nothing came back: the connection failed, was reset, or
	// timed out.
	KindTransport
	// KindStatus: a response came back with a status other than Want.
	KindStatus
	// KindDecode: the response had the right status but its body didn't decode.
	KindDecode
)

// Error is a failed call. Its message is the labelled description (the same
// text it always had), and it unwraps to the underlying error, so errors.Is
// and errors.As see through it to, say, a net.Error timeout.
type Error struct {
	Kind   Kind
	Status int // response status; 0 if no response arrived
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// NoAnswer reports whether the service never gave an answer: the connection
// failed or timed out, or it answered with a 5xx. Callers use it to tell "the
// service is down" from "the service replied and this request was the
// problem".
func (e *Error) NoAnswer() bool {
	return e.Kind == KindTransport || (e.Kind == KindStatus && e.Status >= 500)
}

// Do sends the request and returns the response status (0 if none arrived)
// alongside any error. A non-nil error is a *Error unless OnStatus supplied
// it.
func (c Call) Do(ctx context.Context) (status int, err error) {
	fail := func(kind Kind, status int, format string, args ...any) (int, error) {
		// The label goes in as an argument, not into the format string: it
		// can contain "%" (an escaped path such as /items/a%2Fb).
		err := fmt.Errorf("%s: "+format, append([]any{c.Label}, args...)...)
		return status, &Error{Kind: kind, Status: status, Err: err}
	}

	var reqBody io.Reader
	if c.In != nil {
		b, err := json.Marshal(c.In)
		if err != nil {
			return fail(KindRequest, 0, "encode request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, c.Method, c.URL, reqBody)
	if err != nil {
		return fail(KindRequest, 0, "%w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.In != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := noRedirects(c.Client).Do(req)
	if err != nil {
		return fail(KindTransport, 0, "%w", logx.RedactURLError(err))
	}
	limit := c.MaxResponseBytes
	if limit == 0 {
		limit = DefaultMaxResponseBytes
	}
	body := io.LimitReader(resp.Body, limit)
	defer func() {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, body)
		_ = resp.Body.Close() // fully read: nothing to do about an error
	}()

	if resp.StatusCode != c.Want {
		if c.OnStatus != nil {
			if err := c.OnStatus(resp); err != nil {
				return resp.StatusCode, err
			}
		}
		if c.ErrorDetail == nil {
			return fail(KindStatus, resp.StatusCode, "unexpected status %d", resp.StatusCode)
		}
		return fail(KindStatus, resp.StatusCode, "unexpected status %d: %s", resp.StatusCode, c.ErrorDetail(body))
	}

	if c.Out == nil {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(body).Decode(c.Out); err != nil {
		return fail(KindDecode, resp.StatusCode, "decode response: %w", err)
	}
	return resp.StatusCode, nil
}

// clients maps each client a Call has used to its copy that never follows a
// redirect, so the copy is made once per client rather than on every call.
var clients sync.Map // *http.Client -> *http.Client

// noRedirects returns a copy of client that never follows a redirect, whatever
// it was configured with: the 3xx comes back as an unexpected status instead.
// A compromised or misconfigured service could otherwise send a call to any
// address it can name, internal ones included, and on a 307 or 308 the request
// body would go along too. The copy shares the Transport, and so its
// connections. A client's settings are read once, on its first call.
func noRedirects(client *http.Client) *http.Client {
	if c, ok := clients.Load(client); ok {
		return c.(*http.Client)
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	actual, _ := clients.LoadOrStore(client, &c)
	return actual.(*http.Client)
}
