// Package headerguard screens the headers of every incoming request against a
// policy before the handlers see them: size and count limits, headers that may
// appear only once, a JSON Content-Type on any request with a body, and
// headers that are stripped because nothing here may trust them.
//
// A request that breaks a rule is rejected with a JSON error (400, 415 or 431)
// and a log line, unless the policy is in report mode, in which case it is
// only logged, so the rules can be rolled out without blocking anyone.
//
// Go's HTTP server already rejects malformed headers (control characters,
// invalid names, conflicting Content-Length) and more than 1 MiB of them,
// before any handler runs; those requests never reach this package. Its own
// limits are far lower, so in practice an oversized request gets this
// package's JSON 431, a correlation ID and a log line instead of Go's plain
// text one.
package headerguard

import (
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// Mode says what the guard does with a request that breaks a rule.
type Mode int

const (
	// Enforce rejects the request.
	Enforce Mode = iota
	// Report lets the request through and logs what would have been rejected.
	Report
)

func (m Mode) String() string {
	if m == Report {
		return "report"
	}
	return "enforce"
}

// ParseMode reads a mode by name: "enforce" or "report".
func ParseMode(s string) (Mode, error) {
	switch s {
	case "enforce":
		return Enforce, nil
	case "report":
		return Report, nil
	}
	return Enforce, fmt.Errorf("must be enforce or report, got %q", s)
}

// MaxLimit is the largest any header limit may be set to: Go's HTTP server
// rejects more than 1 MiB of headers before the guard runs, so a higher limit
// could never take effect.
const MaxLimit = 1 << 20

// ParseLimit reads a header limit from configuration: a whole number from 1
// to MaxLimit. Zero and negative values are refused, so a typo can't switch a
// limit off.
func ParseLimit(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("must be a whole number, got %q", s)
	}
	if n < 1 || n > MaxLimit {
		return 0, fmt.Errorf("must be between 1 and %d, got %d", MaxLimit, n)
	}
	return n, nil
}

// Validate reports a policy whose limits can't all be honoured: a limit that
// is zero or negative (which would mean no limit), or a per-header limit above
// the total.
func (p Policy) Validate() error {
	for name, v := range map[string]int{"MaxHeaders": p.MaxHeaders, "MaxHeaderBytes": p.MaxHeaderBytes, "MaxTotalBytes": p.MaxTotalBytes} {
		if v < 1 || v > MaxLimit {
			return fmt.Errorf("%s must be between 1 and %d, got %d", name, MaxLimit, v)
		}
	}
	if p.MaxHeaderBytes > p.MaxTotalBytes {
		return fmt.Errorf("the per-header limit (%d bytes) is larger than the total limit (%d bytes)", p.MaxHeaderBytes, p.MaxTotalBytes)
	}
	return nil
}

// Policy is what the guard enforces. A zero limit means no limit; Validate
// refuses that for policies built from configuration.
type Policy struct {
	Mode Mode

	MaxHeaders     int // header fields, counting each repeated value
	MaxTotalBytes  int // all names and values together
	MaxHeaderBytes int // any one name and value

	// SingleValued lists headers a request may carry at most once.
	SingleValued []string

	// RequireJSONBody rejects a request that has a body unless its
	// Content-Type is application/json (parameters such as charset allowed).
	RequireJSONBody bool

	// Strip lists headers removed before the handlers run, in either mode.
	Strip []string
}

// DefaultPolicy is the policy the API runs with unless configuration changes
// the mode or the limits: limits in line with common proxy defaults (nginx
// allows 8 KiB per header line), and stripping the proxy and method-override
// headers, because nothing here sits behind a trusted proxy that would set
// them. Which headers are single-use or stripped is not configurable: those
// are security decisions, kept in code where changes are reviewed and tested.
func DefaultPolicy() Policy {
	return Policy{
		Mode:            Enforce,
		MaxHeaders:      100,
		MaxTotalBytes:   32 << 10,
		MaxHeaderBytes:  8 << 10,
		SingleValued:    []string{"Authorization", "Content-Type", httpx.RequestIDHeader},
		RequireJSONBody: true,
		Strip: []string{
			"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto",
			"X-Real-Ip", "X-Http-Method-Override", "X-Method-Override", "X-Http-Method",
		},
	}
}

// violation is one broken rule: what to answer, and how to log it.
type violation struct {
	status  int
	reason  string // fixed, for log queries: "header too large", ...
	header  string // the header concerned, if one is
	message string // safe to show the client
}

// Middleware wraps next with the guard. Put it inside httpx.Middleware, so a
// rejected request still gets a correlation ID and its request lines.
func Middleware(logger *slog.Logger, p Policy, next http.Handler) http.Handler {
	strip := make([]string, len(p.Strip))
	for i, h := range p.Strip {
		strip[i] = http.CanonicalHeaderKey(h)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := p.check(r); v != nil {
			attrs := []slog.Attr{
				slog.String(logx.KeyReason, v.reason),
				slog.Int(logx.KeyStatus, v.status),
			}
			if v.header != "" {
				attrs = append(attrs, slog.String(logx.KeyHeader, v.header))
			}
			if p.Mode == Report {
				logger.LogAttrs(r.Context(), slog.LevelInfo, "request headers would be rejected", attrs...)
			} else {
				logger.LogAttrs(r.Context(), slog.LevelInfo, "request headers rejected", attrs...)
				httpx.WriteError(w, v.status, v.message)
				return
			}
		}

		var stripped []string
		for _, h := range strip {
			if _, ok := r.Header[h]; ok {
				delete(r.Header, h)
				stripped = append(stripped, h)
			}
		}
		if len(stripped) > 0 {
			logger.LogAttrs(r.Context(), slog.LevelDebug, "request headers stripped",
				slog.String(logx.KeyHeaders, strings.Join(stripped, ",")))
		}
		next.ServeHTTP(w, r)
	})
}

// check returns the first rule r breaks, or nil.
func (p Policy) check(r *http.Request) *violation {
	count, total := 0, 0
	for name, values := range r.Header {
		for _, v := range values {
			count++
			size := len(name) + len(v)
			total += size
			if p.MaxHeaderBytes > 0 && size > p.MaxHeaderBytes {
				return &violation{
					status: http.StatusRequestHeaderFieldsTooLarge, reason: "header too large", header: name,
					message: fmt.Sprintf("request header %s is larger than %d bytes", logx.Truncate(name, 64), p.MaxHeaderBytes),
				}
			}
		}
	}
	if p.MaxHeaders > 0 && count > p.MaxHeaders {
		return &violation{
			status: http.StatusRequestHeaderFieldsTooLarge, reason: "too many headers",
			message: fmt.Sprintf("a request may have at most %d headers", p.MaxHeaders),
		}
	}
	if p.MaxTotalBytes > 0 && total > p.MaxTotalBytes {
		return &violation{
			status: http.StatusRequestHeaderFieldsTooLarge, reason: "headers too large",
			message: fmt.Sprintf("request headers are larger than %d bytes in total", p.MaxTotalBytes),
		}
	}

	for _, h := range p.SingleValued {
		if len(r.Header.Values(h)) > 1 {
			name := http.CanonicalHeaderKey(h)
			return &violation{
				status: http.StatusBadRequest, reason: "duplicate header", header: name,
				message: fmt.Sprintf("request has more than one %s header", name),
			}
		}
	}

	if p.RequireJSONBody && hasBody(r) {
		ct := r.Header.Get("Content-Type")
		if ct == "" {
			return &violation{
				status: http.StatusUnsupportedMediaType, reason: "missing content type", header: "Content-Type",
				message: "a request body must be sent with Content-Type: application/json",
			}
		}
		if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
			return &violation{
				status: http.StatusUnsupportedMediaType, reason: "unsupported content type", header: "Content-Type",
				message: fmt.Sprintf("Content-Type must be application/json, not %q", logx.Truncate(ct, 100)),
			}
		}
	}
	return nil
}

// hasBody reports whether r carries a body: a declared length, or chunked
// (length unknown).
func hasBody(r *http.Request) bool {
	return r.ContentLength != 0 && r.Body != nil && r.Body != http.NoBody
}
