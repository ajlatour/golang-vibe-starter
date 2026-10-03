package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Probe GETs /healthz on the server listening at addr (a listen address such
// as ":8080") and returns an error unless it answers 200. It backs each
// binary's "healthcheck" subcommand, which container healthchecks run because
// the runtime image has no curl or wget.
func Probe(ctx context.Context, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("probe: bad address %q: %w", addr, err)
	}
	// A wildcard listen address isn't dialable; the server is local.
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	url := "http://" + net.JoinHostPort(host, port) + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe: %s returned %d", url, resp.StatusCode)
	}
	return nil
}
