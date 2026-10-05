package logx_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// BenchmarkLogLine measures one request-scoped log line, like the four every
// request writes, in each format.
func BenchmarkLogLine(b *testing.B) {
	ctx := logx.WithRequestID(context.Background(), "4LSBQEPNJ5NMWEBHAE7IJOPYIX")
	for _, format := range []string{"json", "text"} {
		b.Run(format, func(b *testing.B) {
			logger := logx.New(io.Discard, slog.LevelInfo, format, "api", "bench")
			for b.Loop() {
				logger.InfoContext(ctx, "response sent",
					logx.KeyMethod, "GET", logx.KeyRoute, "GET /items/{id}", logx.KeyPath, "/items/widget",
					logx.KeyStatus, 200, logx.KeyBytes, 142, logx.DurationMS(1500000))
			}
		})
	}
	b.Run("below the level", func(b *testing.B) {
		logger := logx.New(io.Discard, slog.LevelWarn, "json", "api", "bench")
		for b.Loop() {
			logger.InfoContext(ctx, "response sent", logx.KeyMethod, "GET", logx.KeyStatus, 200)
		}
	})
}
