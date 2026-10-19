package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/httpmetrics"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
	"github.com/ajlatour/golang-vibe-starter/internal/validator"
)

// This file and internal/validator are request validation in the service.

// validation is how requests are checked against the request-validation
// service: the checker (nil without VALIDATOR_URL), whether to act on its
// answers, and the readiness check it adds, if any.
type validation struct {
	checker validator.Checker
	enforce func(context.Context) bool
	checks  []httpx.Check
}

// newValidation sets up request validation from VALIDATOR_URL. Whether to
// act on the service's answers, or only log them (shadow mode), is a flag,
// so it's turned on, and back off, at runtime; without flags, it always
// acts. Failing closed, the service can't serve without it, so readiness
// tracks it, while enforcing.
func newValidation(logger *slog.Logger, cfg config, f flags) validation {
	var v validation
	// feature:flags
	v.enforce = func(ctx context.Context) bool { return f.Bool(ctx, validator.FlagEnforce) }
	// end feature:flags
	if cfg.ValidatorURL == nil {
		logger.Warn("request validation is off: no VALIDATOR_URL; every request goes unchecked")
		return v
	}
	logger.Info("using request validation", logx.KeyURL, cfg.ValidatorURL.Redacted(),
		logx.KeyTimeout, cfg.ValidatorTimeout.String(), logx.KeyFailOpen, cfg.ValidatorFailOpen)
	c := validator.New(cfg.ValidatorURL.String(),
		&http.Client{Timeout: cfg.ValidatorTimeout, Transport: newTransport(cfg.Limits.MaxInFlight)})
	if !cfg.ValidatorFailOpen {
		v.checks = append(v.checks, httpx.Check{Name: "validator", Fn: func(ctx context.Context) error {
			if v.enforce != nil && !v.enforce(ctx) {
				return nil // in shadow mode the API serves without it
			}
			return c.Reachable(ctx)
		}})
	}
	v.checker = c
	return v
}

// newValidatorCalls is the metric of calls to the request-validation
// service, by outcome and how long they took: what the check costs, for the
// performance tests and dashboards.
func newValidatorCalls() *httpmetrics.Calls {
	outcomes := make([]string, len(validator.Outcomes))
	for i, o := range validator.Outcomes {
		outcomes[i] = string(o)
	}
	return httpmetrics.NewCalls("validator", "Requests checked against the request-validation service", outcomes...)
}

// withValidation is cfg checking every request with v, recording each
// check's outcome in calls.
func (v validation) withValidation(cfg handlerConfig, failOpen bool, calls *httpmetrics.Calls) handlerConfig {
	cfg.Validator, cfg.ValidatorFailOpen, cfg.ValidatorEnforce = v.checker, failOpen, v.enforce
	cfg.ValidatorObserve = func(o validator.Outcome, d time.Duration) { calls.Observe(string(o), d) }
	return cfg
}

// validate puts the request-validation check in front of next, if there's a
// validator: after authentication, so it knows who's calling (the key's
// name), and after the rate limit, so a client's flood never reaches it. The
// probes are exempt, like from the limits.
func validate(logger *slog.Logger, cfg handlerConfig, probes []string, next http.Handler) http.Handler {
	if cfg.Validator == nil {
		return next
	}
	return validator.Middleware(logger, cfg.Validator, validator.Policy{
		FailOpen: cfg.ValidatorFailOpen,
		Enforce:  cfg.ValidatorEnforce,
		Observe:  cfg.ValidatorObserve,
		Retry:    time.Second,
		Exempt:   probes,
		Client:   func(r *http.Request) string { return apikey.Name(r.Context()) },
	}, next)
}
