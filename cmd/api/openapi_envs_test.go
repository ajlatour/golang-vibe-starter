package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
	"github.com/ajlatour/golang-vibe-starter/internal/validator"
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
//	refused     the request-validation service refuses every request (403)
//	unvalidated the request-validation service can't answer, failing closed (503)
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
		cfg.Validator = blockingCheck{entered: make(chan struct{}), release: make(chan struct{})}
	case "refused":
		cfg.Validator = fixedDecision{}
	case "unvalidated":
		cfg.Validator = validator.New(nettest.DeadURL(), &http.Client{Timeout: time.Second})
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
		block := cfg.Validator.(blockingCheck)
		stuck := httptest.NewRequest("GET", "/", nil)
		stuck.Header.Set("Authorization", "Bearer "+testWriteKey)
		done := make(chan struct{})
		go func() { h.ServeHTTP(httptest.NewRecorder(), stuck); close(done) }()
		<-block.entered
		t.Cleanup(func() { close(block.release); <-done })
	}
	return h, ""
}

// blockingCheck is a validator.Checker that holds every check until release
// is closed, after saying on entered that one has arrived.
type blockingCheck struct{ entered, release chan struct{} }

func (b blockingCheck) Check(context.Context, validator.Request) (validator.Decision, error) {
	b.entered <- struct{}{}
	<-b.release
	return validator.Decision{Allow: true}, nil
}

// fixedDecision is a validator.Checker that answers every request with
// itself: the zero value refuses.
type fixedDecision validator.Decision

func (d fixedDecision) Check(context.Context, validator.Request) (validator.Decision, error) {
	return validator.Decision(d), nil
}
