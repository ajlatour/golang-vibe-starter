package main

import (
	"net/http"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// testResources is what the spec tests need of each resource the API serves
// (resources), from its own test file (items_test.go). make resource adds
// one here.
var testResources = []testResource{
	itemsTest,
}

// testResource is what the spec tests need of a resource.
type testResource struct {
	// path is its collection's, such as "/items"; its operations are on it
	// and below it.
	path string
	// okBody is a request body its writes accept.
	okBody string
	// routes is its routes on a store for a scenario environment
	// (handlerFor), and that store's readiness checks: for "ok" and
	// "flagOn", holding one entity, whose ID is seededID, with its feature
	// flags all on for "flagOn"; "paged", three; "internal", failing with
	// errBoom; "badgw" and "timeout", on the upstream up; for the others,
	// empty.
	routes func(t *testing.T, env string, up upstream) (routes, []httpx.Check)
	// guide is its routes on a store holding what the WireMock stubs serve,
	// for docs/curl.md's examples.
	guide func(t *testing.T) routes
	// scenarios are its own: its successes, and the client's mistakes it
	// answers (TestOpenAPIMatchesBehavior).
	scenarios func(t *testing.T, s *spec) []scenario
}

// upstream is where a store that calls one finds it.
type upstream struct {
	url    string
	client *http.Client
}

// seededID is the ID of the entity each resource holds in the "ok" and
// "flagOn" environments, which scenarios name as seededPlaceholder.
const seededID = "seeded"
