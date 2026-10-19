package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
	"github.com/ajlatour/golang-vibe-starter/internal/telemetry"
	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

// serviceName is the service's name in its traces, unless
// OTEL_SERVICE_NAME says otherwise.
const serviceName = "golang-vibe-starter"

// newSpans returns what records the service's spans, and stop, which
// exports the last of them, within timeout: the OpenTelemetry SDK
// (internal/telemetry) when there's an endpoint to export them to
// (TracesEndpoint); otherwise nothing, and the service records no spans.
// This file and internal/telemetry are all of OpenTelemetry in the service:
// docs/tracing.md#removing-it.
func newSpans(ctx context.Context, logger *slog.Logger, cfg config, timeout time.Duration) (tracectx.Spans, func(), error) {
	if cfg.TracesEndpoint == nil {
		return nil, func() {}, nil
	}
	s, err := telemetry.New(ctx, cfg.TracesEndpoint.String(), serviceName, buildVersion(), func(err error) {
		logger.Warn("tracing failed", logx.KeyError, err)
	})
	if err != nil {
		return nil, nil, err
	}
	logger.Info("using tracing", logx.KeyURL, cfg.TracesEndpoint.Redacted())
	return s, func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			logger.Warn("tracing failed", logx.KeyError, err)
		}
	}, nil
}
