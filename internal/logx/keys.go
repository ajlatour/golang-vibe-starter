package logx

// Log field names. This is the one list: every log line uses these names for
// these values, so a single query finds them all, and a test fails the build
// if code passes a raw string as a key instead. docs/logging.md documents each.
const (
	// On every line.
	KeyService   = "service"
	KeyVersion   = "version"
	KeyRequestID = "request_id" // within a request, added from its context

	// The request and response (see logRequests).
	KeyMethod = "method"
	KeyRoute  = "route" // matched pattern, e.g. "GET /items/{id}"
	KeyPath   = "path"  // URL path only, never the query string
	// KeyContentLength is the request body size the client declared, or -1
	// if unknown (e.g. chunked). Only the number is logged, never the body.
	KeyContentLength = "content_length"
	// KeyClientIP is the address the request's connection came from (with
	// httpx.WithClientIP): a load balancer's, behind one.
	KeyClientIP = "client_ip"
	KeyStatus   = "status"
	// KeyResponseError is the error message a response sent the client
	// ({"error": "..."}): why a request was refused, or what failed.
	KeyResponseError = "response_error"
	// KeyResponseCode is the error response's code (httpx.Code): what it
	// means, stable, to group and alert on.
	KeyResponseCode = "response_code"
	KeyBytes        = "bytes"
	KeyDurationMS   = "duration_ms"

	// Failures.
	KeyError = "error"
	KeyPanic = "panic"
	KeyStack = "stack"

	// Upstream calls. Prefixed "upstream_" so they can't be confused with the
	// inbound request's method, path and status.
	KeyUpstream           = "upstream" // "METHOD /path" of the call
	KeyUpstreamStatus     = "upstream_status"
	KeyUpstreamDurationMS = "upstream_duration_ms"

	// Header screening (internal/headerguard).
	KeyReason  = "reason"  // why a request was rejected, e.g. "header too large"
	KeyHeader  = "header"  // the header concerned
	KeyHeaders = "headers" // header names, comma-separated

	KeyAPIKey  = "api_key"  // an API key's configured name, never the key
	KeyAPIKeys = "api_keys" // the configured keys' names, comma-separated
	KeyReads   = "reads"    // whether reads need a key: required or open

	KeyMaxInFlight = "max_in_flight" // the cap on requests in flight
	KeyRateLimit   = "rate_limit"    // requests per second per client
	KeyRateBurst   = "rate_burst"

	KeyMode           = "mode" // enforce or report
	KeyMaxHeaders     = "max_headers"
	KeyMaxHeaderBytes = "max_header_bytes"
	KeyMaxTotalBytes  = "max_total_bytes"

	// Feature flags.
	KeyFlag      = "flag"
	KeyDefault   = "default"
	KeyFallbacks = "fallbacks"
	KeyCacheTTL  = "cache_ttl"

	// Request validation (internal/validator).
	KeyFailOpen  = "fail_open" // whether requests go through when it can't answer
	KeyEnforcing = "enforcing" // whether its answers are acted on, or only logged (shadow)
	KeyFailures  = "failures"  // requests answered with the failure mode during an outage

	// Startup and shutdown.
	KeyURL     = "url"
	KeyTimeout = "timeout"
	KeyAddr    = "addr"

	// The Go runtime: what it runs with, and what it's using.
	KeyGoVersion          = "go_version"
	KeyGOMAXPROCS         = "gomaxprocs"
	KeyMemoryLimitBytes   = "memory_limit_bytes"
	KeyStatsInterval      = "stats_interval"
	KeyHeapBytes          = "heap_bytes"
	KeyRuntimeMemoryBytes = "runtime_memory_bytes"
	KeyGoroutines         = "goroutines"
	KeyGCCycles           = "gc_cycles"
)

// MaxLoggedPath caps the request and upstream paths written to logs, which
// are chosen by the caller and could otherwise be up to the server's header
// limit (1 MiB) per line.
const MaxLoggedPath = 512
