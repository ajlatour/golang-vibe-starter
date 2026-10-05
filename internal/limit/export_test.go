package limit

import (
	"net/http"
	"time"
)

// PerClientAt is PerClient with a clock the tests control.
func PerClientAt(p Policy, now func() time.Time, next http.Handler) http.Handler {
	return perClient(p, now, next)
}
