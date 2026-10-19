package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
	// feature:validation
	"github.com/ajlatour/golang-vibe-starter/internal/validator"
	// end feature:validation
)

// fixedFlags is a resource's Flags with one answer for every flag.
type fixedFlags bool

func (f fixedFlags) Bool(context.Context, string) bool { return bool(f) }

// errBoom is the plain error each resource's "internal" store fails with.
var errBoom = errors.New("boom")

// handlerFor builds the real service handler for an environment, with every
// resource's routes on a store for it (testResource.routes):
//
//	ok          each resource holds one entity, seededID; flags off
//	flagOn      the same, with every flag on
//	paged       three of each, so a page of two has a next
//	ratelimited the test client (its key) has spent its rate (429)
//	overloaded  the one request the service takes at once is stuck (503)
//	refused     with request validation, its service refuses every request (403),
//	unvalidated or can't answer, failing closed (503)
//	internal    stores that fail with a plain error (a bug here: 500)
//	badgw       an upstream that refuses connections (502)
//	timeout     an upstream that never answers (504)
//
// It returns the handler and the seeded entities' ID.
func handlerFor(t *testing.T, env string) (http.Handler, string) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	cfg := handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t)}
	var up upstream
	switch env {
	case "ok", "flagOn", "paged", "internal":
	case "ratelimited":
		// The test client has its one token, to spend below, and gets
		// another in about 11 days.
		cfg.Limits = limit.Policy{Rate: 1e-6, Burst: 1}
	case "overloaded":
		// The service takes one request at once, held below.
		cfg.Limits = limit.Policy{MaxInFlight: 1}
	// feature:validation
	case "refused":
		cfg.Validator = fixedDecision{}
	case "unvalidated":
		cfg.Validator = validator.New(nettest.DeadURL(), &http.Client{Timeout: time.Second})
	// end feature:validation
	case "badgw":
		up = upstream{url: nettest.DeadURL(), client: &http.Client{Timeout: time.Second}}
	case "timeout":
		hang := httptest.NewServer(http.HandlerFunc(nettest.Hang))
		t.Cleanup(hang.Close)
		up = upstream{url: hang.URL, client: &http.Client{Timeout: 50 * time.Millisecond}}
	default:
		t.Fatalf("unknown environment %q", env)
	}

	var (
		all    []routes
		checks []httpx.Check
	)
	for _, r := range testResources {
		rt, c := r.routes(t, env, up)
		all = append(all, rt)
		checks = append(checks, c...)
	}
	if len(checks) > 0 {
		cfg.Readiness = httpx.NewReadiness(logger, time.Second, time.Second, checks...)
	}
	h := newHandler(logger, cfg, all...)

	switch env {
	case "ok", "flagOn":
		return h, seededID
	case "ratelimited":
		spend := httptest.NewRequest("GET", "/", nil)
		spend.Header.Set("Authorization", "Bearer "+testWriteKey)
		h.ServeHTTP(httptest.NewRecorder(), spend)
	case "overloaded":
		// A create whose body never ends: the handler is reading it, so the
		// request holds the one slot. The first byte is taken only once the
		// handler reads, so when the write returns, it's there.
		body, more := io.Pipe()
		stuck := httptest.NewRequest("POST", testResources[0].path, body)
		stuck.Header.Set("Authorization", "Bearer "+testWriteKey)
		stuck.Header.Set("Content-Type", "application/json")
		done := make(chan struct{})
		go func() { h.ServeHTTP(httptest.NewRecorder(), stuck); close(done) }()
		if _, err := more.Write([]byte("{")); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { more.Close(); <-done })
	}
	return h, ""
}

// feature:validation
// fixedDecision is a validator.Checker that answers every request with
// itself: the zero value refuses.
type fixedDecision validator.Decision

func (d fixedDecision) Check(context.Context, validator.Request) (validator.Decision, error) {
	return validator.Decision(d), nil
}

// end feature:validation
