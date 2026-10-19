package httpx

// Code says what an error response means, for a client to act on. It's
// stable: a client can switch on it, where the message beside it is for
// people and may be reworded. Two errors with one status can mean different
// things (a 403 for a read-only key, or one the validation service refused),
// and the code tells them apart.
//
// This is the one list. docs/openapi.json lists the same codes in the Error
// schema, and cmd/api's spec tests hold the two to each other and check every
// response's code is one of them. A new error gets a code here first.
type Code string

const (
	// 400: the body isn't usable: malformed JSON, the wrong shape or types,
	// an unknown field, empty, or over 1 MiB.
	CodeInvalidBody Code = "invalid_body"
	// 400: a header that may appear only once appears more than once.
	CodeDuplicateHeader Code = "duplicate_header"
	// 400: an API-Version the API doesn't serve.
	CodeUnsupportedAPIVersion Code = "unsupported_api_version"
	// 400: a query parameter that isn't valid: a list's limit or cursor.
	CodeInvalidParameter Code = "invalid_parameter"
	// 400: a request target that isn't a path, such as OPTIONS *.
	CodeInvalidTarget Code = "invalid_target"
	// 401: no API key, or not sent as a bearer token.
	CodeAPIKeyRequired Code = "api_key_required"
	// 401: an API key that isn't a valid one.
	CodeAPIKeyInvalid Code = "api_key_invalid"
	// 403: a write with a key that may only read.
	CodeReadOnlyKey Code = "read_only_key"
	// feature:validation
	// 403: the request-validation service refused the request.
	CodeValidationRefused Code = "validation_refused"
	// end feature:validation
	// 404: no item has that ID, or no route has that path.
	CodeNotFound Code = "not_found"
	// 405: the path has routes, but none for that method; Allow lists them.
	CodeMethodNotAllowed Code = "method_not_allowed"
	// 412: a write's If-Match no longer matches the item: it changed since
	// the client read it.
	CodePreconditionFailed Code = "precondition_failed"
	// 415: a body that isn't sent as application/json.
	CodeUnsupportedMediaType Code = "unsupported_media_type"
	// 422: a well-formed body with a value that breaks a rule.
	CodeInvalidField Code = "invalid_field"
	// 429: over the client's rate limit.
	CodeRateLimited Code = "rate_limited"
	// 431: the request's headers are over a limit.
	CodeHeadersTooLarge Code = "headers_too_large"
	// 499: the client went away before the answer (logged; no one reads it).
	CodeClientClosed Code = "client_closed"
	// 500: an unexpected failure in this service.
	CodeInternal Code = "internal"
	// 502: the upstream couldn't give a usable answer.
	CodeUpstreamFailed Code = "upstream_failed"
	// 503: the service is serving as many requests as it takes at once.
	CodeBusy Code = "busy"
	// 503: the service is shutting down, and stopped the request before it
	// finished: retry it, and another instance will serve it.
	CodeShuttingDown Code = "shutting_down"
	// feature:validation
	// 503: the request-validation service can't answer, and the API fails
	// closed.
	CodeValidationUnavailable Code = "validation_unavailable"
	// end feature:validation
	// 504: the upstream didn't answer in time.
	CodeUpstreamTimeout Code = "upstream_timeout"
)
