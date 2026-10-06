package validator

import (
	"log/slog"
	"net/http"
	"time"
)

// MiddlewareAt is Middleware with a clock the tests control.
func MiddlewareAt(logger *slog.Logger, c Checker, p Policy, now func() time.Time, next http.Handler) http.Handler {
	return middleware(logger, c, p, now, next)
}
