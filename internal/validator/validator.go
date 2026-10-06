// Package validator asks an external request-validation service about every
// request before the routes see it, and refuses those it doesn't allow. It's
// a scaffold for a check the API can't serve without: a fraud or entitlement
// service, a policy engine, a token introspection endpoint.
//
// The service is called at VALIDATOR_URL with POST /validate and a Request,
// and answers a Decision. That contract is a placeholder: change Request to
// carry what your service needs, and Client.Check to speak its API; the
// middleware, the failure handling and the logging stay as they are.
//
// Because the check is on every request's path, the client has a short
// timeout, and the middleware is careful when the service fails: it refuses
// requests (fail closed, the default) or lets them through (fail open), and
// while the service is down it asks again at most once per Policy.Retry,
// answering every request in between at once instead of each waiting out the
// timeout. See ADR-0032.
package validator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ajlatour/golang-vibe-starter/internal/jsoncall"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// Request is what the service is asked about a request. A placeholder:
// carry what your service needs to decide. Never the request's credentials
// or body; the client's name is enough to say who's asking.
type Request struct {
	Client string `json:"client"` // the caller's name, e.g. its API key's; "" if unknown
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Decision is the service's answer.
type Decision struct {
	Allow bool `json:"allow"`
	// Reason says why a request was refused. It's logged, never sent to
	// the client: it may describe the service's rules.
	Reason string `json:"reason"`
}

// Checker decides about a request. Client is the one the service uses; tests
// use their own.
type Checker interface {
	Check(ctx context.Context, req Request) (Decision, error)
}

// maxResponseBytes caps how much of the service's answer is read.
const maxResponseBytes = 64 << 10 // 64 KiB: a decision is small

// Client is a Checker that calls the validation service over HTTP.
type Client struct {
	baseURL string
	client  *http.Client
}

var _ Checker = (*Client)(nil)

// New returns a client for the service at baseURL, using client, which
// should have a short timeout: the check is on every request's path.
func New(baseURL string, client *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

// Check asks the service about req. An error means the service gave no
// usable answer (unreachable, timed out, an error status, a body that won't
// decode), not that it refused: a refusal is a Decision.
func (c *Client) Check(ctx context.Context, req Request) (Decision, error) {
	var d Decision
	_, err := jsoncall.Call{
		Client:           c.client,
		Method:           http.MethodPost,
		URL:              c.baseURL + "/validate",
		Label:            "validator POST /validate",
		In:               req,
		Out:              &d,
		Want:             http.StatusOK,
		MaxResponseBytes: maxResponseBytes,
		ErrorDetail:      errorDetail,
	}.Do(ctx)
	return d, err
}

// errorDetail quotes the service's {"error": "..."} message, bounded.
func errorDetail(body io.Reader) string {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(body).Decode(&e)
	return fmt.Sprintf("%q", logx.Truncate(e.Error, 200))
}

// Reachable reports whether the service answers HTTP at its base URL, for
// readiness: any response counts, an error status included, as for the
// upstream (ADR-0021). It goes through the client's transport alone, so a
// redirect is an answer, never followed.
func (c *Client) Reachable(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/", nil)
	if err != nil {
		return err
	}
	transport := c.client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return logx.RedactURLError(err)
	}
	_ = resp.Body.Close()
	return nil
}
