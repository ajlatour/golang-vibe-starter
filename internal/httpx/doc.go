// Package httpx is the HTTP service plumbing shared by every binary in this
// repo: graceful serving, middleware (correlation IDs, request logging, panic
// recovery), JSON helpers, and the container healthcheck probe. The logging
// toolkit it builds on lives in internal/logx, and configuration in
// internal/envconfig.
package httpx
