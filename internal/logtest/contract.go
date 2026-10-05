package logtest

// Message is what a log message promises: the levels it may be logged at and
// the fields it always carries, besides the common ones (time, level, msg,
// service, version, and request_id within a request).
type Message struct {
	Levels []string
	Keys   []string
}

// Contract is the service's log schema: every message the code can emit, with
// its promises. It is the one place that says what each message is for, and
// tests hold the rest to it:
//
//   - the log contract test (cmd/api) runs the service and checks every line
//     it writes against this table;
//   - the schema tests (internal/logx) check that every message in the code is
//     listed here, that nothing listed here is stale, that every field named
//     here is a declared Key, and that the Messages table in docs/logging.md says the
//     same thing.
//
// Adding a log line therefore means adding it here, and to that table.
//
// "request received" and "response sent" have more precise level rules than a
// set of levels can say; the contract test checks those directly: both are
// DEBUG for the healthcheck, and "response sent" is ERROR for a 5xx.
var Contract = map[string]Message{
	// Lifecycle: logged outside any request, so no request_id.
	"using upstream":      {[]string{"INFO"}, []string{"url", "timeout"}},
	"using feature flags": {[]string{"INFO"}, []string{"url", "timeout"}},
	"feature flags: no FLAGS_URL, using default values only": {[]string{"INFO"}, nil},
	"server started": {[]string{"INFO"}, []string{"addr"}},
	"shutting down":  {[]string{"INFO"}, []string{"timeout"}},
	"server stopped": {[]string{"INFO"}, nil},

	// The four ends of a request, in order.
	"request received":           {[]string{"INFO", "DEBUG"}, []string{"method", "path", "content_length"}},
	"upstream request sent":      {[]string{"INFO"}, []string{"upstream"}},
	"upstream response received": {[]string{"INFO"}, []string{"upstream", "upstream_status", "upstream_duration_ms"}},
	"response sent":              {[]string{"INFO", "DEBUG", "ERROR"}, []string{"method", "route", "path", "status", "bytes", "duration_ms"}},

	// What can go wrong in between.
	"internal error":             {[]string{"ERROR"}, []string{"method", "route", "path", "error"}},
	"client disconnected":        {[]string{"INFO"}, []string{"method", "route", "path", "error"}},
	"panic serving request":      {[]string{"ERROR"}, []string{"method", "route", "path", "panic", "stack"}},
	"upstream request failed":    {[]string{"WARN"}, []string{"upstream", "upstream_duration_ms", "error"}},
	"upstream request cancelled": {[]string{"DEBUG"}, []string{"upstream", "upstream_duration_ms", "error"}},

	// Header screening (internal/headerguard).
	"request headers rejected":          {[]string{"INFO"}, []string{"reason", "status"}},
	"request headers would be rejected": {[]string{"INFO"}, []string{"reason", "status"}},
	"request headers stripped":          {[]string{"DEBUG"}, []string{"headers"}},

	// Feature flags.
	"flag service unavailable, using default flag values": {[]string{"WARN"}, []string{"flag", "error"}},
	"flag service recovered":                              {[]string{"INFO"}, []string{"fallbacks", "duration_ms"}},
	"flag evaluation failed, using default":               {[]string{"WARN", "DEBUG"}, []string{"flag", "default", "error"}},
	"flag has no configured default, using false":         {[]string{"ERROR"}, []string{"flag"}},
}

// OutsideRequests lists the messages that are logged outside any request, and
// so legitimately have no request_id. Every other message has one.
var OutsideRequests = []string{
	"using upstream", "using feature flags", "feature flags: no FLAGS_URL, using default values only",
	"server started", "shutting down", "server stopped",
}
