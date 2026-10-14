package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// scenario is one request, with the status the spec says it gets.
type scenario struct {
	name         string
	env          string
	method, path string // path may contain seededPlaceholder
	op           string // the spec operation it exercises, e.g. "GET /items/{id}"
	body         string
	want         int
	check        func(t *testing.T, body []byte)

	// header is added to the request; a header with nil values is left out.
	// A request gets the test write key, and with a body, Content-Type:
	// application/json, unless header says otherwise.
	header http.Header

	// exempt says why a body the schema calls valid is refused anyway, for a
	// rule the schema can't express. Empty for every other scenario.
	exempt string
}

// scenarios covers every documented response of every operation: each
// resource's own (testResources), and for every operation, the failures
// every route shares, generated from the spec's own list of operations.
func scenarios(t *testing.T, s *spec) []scenario {
	t.Helper()
	list := []scenario{
		{name: "healthz", env: "ok", method: "GET", path: "/healthz", op: "GET /healthz", want: 200},
		{name: "ready", env: "ok", method: "GET", path: "/readyz", op: "GET /readyz", want: 200},
		{name: "not ready: the upstream can't be reached", env: "badgw", method: "GET", path: "/readyz", op: "GET /readyz", want: 503},
	}

	// The header rules (internal/headerguard), for every operation: too many
	// headers (431), a duplicated single-use header (400), and for a body sent
	// as anything but JSON (415).
	tooMany := http.Header{}
	for i := range 101 {
		tooMany.Set(fmt.Sprintf("X-Extra-%d", i), "v")
	}
	for _, o := range s.operations(t) {
		path := strings.ReplaceAll(o.Path, "{id}", "x")
		method := strings.ToUpper(o.Method)
		body := ""
		if _, has := o.Op["requestBody"]; has {
			body = okBody(t, o)
		}
		list = append(list,
			scenario{
				name: o.Key() + " with too many headers", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: tooMany, want: http.StatusRequestHeaderFieldsTooLarge,
			},
			scenario{
				name: o.Key() + " with two correlation IDs", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: http.Header{"Client-Correlation-Id": {"a", "b"}}, want: http.StatusBadRequest,
				exempt: "refused for its headers, not its body",
			},
		)
		if body != "" {
			list = append(list, scenario{
				name: o.Key() + " with a text/plain body", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: http.Header{"Content-Type": {"text/plain"}}, want: http.StatusUnsupportedMediaType,
			})
		}

		// Versioning (httpx.APIVersion): every operation but the probes
		// refuses a version the API doesn't serve; the probes answer.
		unsupported := http.Header{"Api-Version": {"99"}}
		if o.Path == "/healthz" || o.Path == "/readyz" {
			list = append(list, scenario{
				name: o.Key() + " with an unsupported API version", env: "ok", method: method, path: path, op: o.Key(),
				header: unsupported, want: http.StatusOK,
			})
		} else {
			list = append(list, scenario{
				name: o.Key() + " with an unsupported API version", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: unsupported, want: http.StatusBadRequest,
				exempt: "refused for its version, not its body",
			})
		}

		// Authentication (internal/apikey): every operation the spec says
		// needs a key refuses a request without one, and a read-only key's
		// writes.
		if sec, ok := o.Op["security"].([]any); ok && len(sec) == 0 {
			continue // exempt, like the healthcheck
		}
		list = append(list, scenario{
			name: o.Key() + " without an API key", env: "ok", method: method, path: path, op: o.Key(),
			body: body, header: http.Header{"Authorization": nil}, want: http.StatusUnauthorized,
			exempt: "refused for its key, not its body",
		})
		if method != http.MethodGet {
			list = append(list, scenario{
				name: o.Key() + " with a read-only key", env: "ok", method: method, path: path, op: o.Key(),
				body: body, header: http.Header{"Authorization": {"Bearer " + testReadKey}}, want: http.StatusForbidden,
				exempt: "refused for its key, not its body",
			})
		}
	}

	// The limits (internal/limit), for every operation but the healthcheck,
	// which is exempt: over the client's rate (429), and with the service
	// full (503).
	for _, o := range s.operations(t) {
		if o.Path == "/healthz" || o.Path == "/readyz" {
			continue // the probes are exempt, and reach no store
		}
		path := strings.ReplaceAll(o.Path, "{id}", "x")
		body := ""
		if _, has := o.Op["requestBody"]; has {
			body = okBody(t, o)
		}
		for env, want := range map[string]int{
			"ratelimited": http.StatusTooManyRequests, "overloaded": http.StatusServiceUnavailable,
			// Request validation (internal/validator): refused, and unable
			// to answer while failing closed.
			"refused": http.StatusForbidden, "unvalidated": http.StatusServiceUnavailable,
		} {
			list = append(list, scenario{
				name: o.Key() + " " + env, env: env, method: strings.ToUpper(o.Method), path: path, op: o.Key(),
				body: body, want: want, exempt: "refused for its rate, not its body",
			})
		}
	}

	// The three ways the service itself or its upstream can fail, for every
	// operation that reaches the store.
	failures := map[string]int{"internal": 500, "badgw": 502, "timeout": 504}
	for _, o := range s.operations(t) {
		if o.Path == "/healthz" || o.Path == "/readyz" {
			continue // the probes are exempt, and reach no store
		}
		path := strings.ReplaceAll(o.Path, "{id}", "x")
		body := ""
		if _, has := o.Op["requestBody"]; has {
			body = okBody(t, o)
		}
		for env, want := range failures {
			list = append(list, scenario{
				name: o.Key() + " fails with " + strconv.Itoa(want), env: env,
				method: strings.ToUpper(o.Method), path: path, op: o.Key(), body: body, want: want,
			})
		}
	}
	for _, r := range testResources {
		list = append(list, r.scenarios(t, s)...)
	}
	return list
}

// okBody is a request body o accepts: its resource's.
func okBody(t *testing.T, o operation) string {
	t.Helper()
	for _, r := range testResources {
		if o.Path == r.path || strings.HasPrefix(o.Path, r.path+"/") {
			return r.okBody
		}
	}
	t.Fatalf("%s: no resource in testResources serves it", o.Key())
	return ""
}
