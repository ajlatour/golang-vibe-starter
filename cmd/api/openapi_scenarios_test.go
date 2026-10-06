package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
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

// scenarios covers every documented response of every operation. The failure
// statuses are generated from the spec's own list of item operations.
func scenarios(t *testing.T, s *spec) []scenario {
	t.Helper()
	input := s.lookupSchema(t, "ItemInput")
	maxName := int(input["properties"].(map[string]any)["name"].(map[string]any)["maxLength"].(float64))
	maxDesc := int(input["properties"].(map[string]any)["description"].(map[string]any)["maxLength"].(float64))
	name := func(n int) string { return strings.Repeat("a", n) }
	okBody := `{"name":"widget","description":"d"}`

	list := []scenario{
		{name: "healthz", env: "ok", method: "GET", path: "/healthz", op: "GET /healthz", want: 200},

		{name: "list", env: "ok", method: "GET", path: "/items", op: "GET /items", want: 200,
			check: func(t *testing.T, body []byte) {
				var got map[string]any
				json.Unmarshal(body, &got)
				if _, present := got["total"]; present {
					t.Errorf("total is present with the flag off, but the spec says it appears only when the flag is on: %s", body)
				}
			}},
		{name: "list with the flag on", env: "flagOn", method: "GET", path: "/items", op: "GET /items", want: 200,
			check: func(t *testing.T, body []byte) {
				var got struct {
					Items []any
					Total *int
				}
				json.Unmarshal(body, &got)
				if got.Total == nil || *got.Total != len(got.Items) {
					t.Errorf("total = %v with %d items, want the count: %s", got.Total, len(got.Items), body)
				}
			}},

		{name: "create", env: "ok", method: "POST", path: "/items", op: "POST /items", body: okBody, want: 201},
		{name: "create at the name length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"` + name(maxName) + `"}`, want: 201},
		{name: "create over the name length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"` + name(maxName+1) + `"}`, want: 422},
		{name: "create at the description length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"x","description":"` + name(maxDesc) + `"}`, want: 201},
		{name: "create over the description length limit", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"x","description":"` + name(maxDesc+1) + `"}`, want: 422},
		{name: "create with a blank name", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"   "}`, want: 422},
		{name: "create without a name", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"description":"x"}`, want: 422},
		{name: "create with malformed JSON", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":`, want: 400},
		{name: "create with an empty body", env: "ok", method: "POST", path: "/items", op: "POST /items", body: ``, want: 400},
		{name: "create with an unknown field", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":"x","colour":"red"}`, want: 400},
		{name: "create with the wrong type", env: "ok", method: "POST", path: "/items", op: "POST /items", body: `{"name":1}`, want: 400},
		{name: "create over the body limit", env: "ok", method: "POST", path: "/items", op: "POST /items",
			body: `{"name":"x","description":"` + strings.Repeat("a", httpx.MaxBodyBytes) + `"}`, want: 400,
			exempt: "the 1 MiB body limit is stated in the description; JSON Schema can't express it"},

		{name: "get", env: "ok", method: "GET", path: "/items/" + seededPlaceholder, op: "GET /items/{id}", want: 200},
		{name: "get unknown", env: "ok", method: "GET", path: "/items/nope", op: "GET /items/{id}", want: 404},

		{name: "update", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{"name":"renamed"}`, want: 200},
		{name: "update with malformed JSON", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{`, want: 400},
		{name: "update unknown", env: "ok", method: "PUT", path: "/items/nope", op: "PUT /items/{id}", body: okBody, want: 404},
		{name: "update with a blank name", env: "ok", method: "PUT", path: "/items/" + seededPlaceholder, op: "PUT /items/{id}", body: `{"name":""}`, want: 422},

		{name: "delete", env: "ok", method: "DELETE", path: "/items/" + seededPlaceholder, op: "DELETE /items/{id}", want: 204},
		{name: "delete unknown", env: "ok", method: "DELETE", path: "/items/nope", op: "DELETE /items/{id}", want: 404},

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
			body = okBody
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
			body = okBody
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
			body = okBody
		}
		for env, want := range failures {
			list = append(list, scenario{
				name: o.Key() + " fails with " + strconv.Itoa(want), env: env,
				method: strings.ToUpper(o.Method), path: path, op: o.Key(), body: body, want: want,
			})
		}
	}
	return list
}
