package logx

import (
	"errors"
	"log/slog"
	"net/url"
	"time"
	"unicode/utf8"
)

// DurationMS returns d as a duration_ms attribute: milliseconds as a decimal
// number, which reads and aggregates better than slog's nanosecond integers.
func DurationMS(d time.Duration) slog.Attr {
	return DurationAttr(KeyDurationMS, d)
}

// DurationAttr is DurationMS under a different key, such as
// "upstream_duration_ms". Keys should end in _ms.
func DurationAttr(key string, d time.Duration) slog.Attr {
	return slog.Float64(key, float64(d.Microseconds())/1000)
}

// Truncate shortens s to at most n runes for logging, ending it with "…" when
// it cuts. Use it for any logged value that comes from outside the service.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// RedactURLError masks credentials in the URL that net/http puts in its
// transport errors (*url.Error). The client already hides a password but
// leaves the username, and the rule for logs is that URLs go through
// (*url.URL).Redacted. Wrap any error from http.Client.Do before it can reach
// a log line.
func RedactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	u, perr := url.Parse(ue.URL)
	if perr != nil {
		return err
	}
	return &url.Error{Op: ue.Op, URL: u.Redacted(), Err: ue.Err}
}

// AttrsError is implemented by errors that know extra fields worth
// logging, such as the status of a failed upstream call. ServerError adds
// them to its "internal error" line.
type AttrsError interface {
	error
	LogAttrs() []slog.Attr
}

// ErrorAttrs returns the error attribute for err, followed by the fields of
// the first AttrsError in its chain, if any.
func ErrorAttrs(err error) []slog.Attr {
	attrs := []slog.Attr{slog.Any(KeyError, err)}
	if la, ok := errors.AsType[AttrsError](err); ok {
		attrs = append(attrs, la.LogAttrs()...)
	}
	return attrs
}
