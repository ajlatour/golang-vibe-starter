// Package integration holds the end-to-end feature tests: Gherkin scenarios
// in features/, run with Godog against the real stack (the API container and
// its stubbed dependencies) started by docker compose. It is a separate Go module so
// that Godog, a test-only dependency, never becomes a dependency of the
// service itself, and so `go test ./...` at the repository root stays fast and
// needs no Docker.
//
// Test code never calls Docker: the stack is started before a run, and the
// tests talk HTTP to it, and edit files it reads (flags_test.go).
//
// Run it with:
//
//	docker compose -f compose.yaml -f compose.integration.yaml up -d --build --wait
//	go test -C integration ./... -v
//	docker compose -f compose.yaml -f compose.integration.yaml down
//
// Godog's own flags work too, with a "godog." prefix, e.g.
// -godog.tags=@smoke or -godog.format=progress.
package integration

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// opts are the suite's settings. Scenarios share the stack's state (WireMock
// faults, flag values), so they run one at a time.
var opts = godog.Options{
	Format:      "pretty",
	Paths:       []string{"features"},
	Strict:      true, // an undefined or pending step fails the run
	Concurrency: 1,
}

// BindFlags, not BindCommandLineFlags: in this Godog version the latter binds
// to spf13/pflag's flag set, which `go test` never parses.
func init() {
	godog.BindFlags("godog.", flag.CommandLine, &opts)
}

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// stack is where the services under test listen. The defaults match
// compose.yaml; override them to point the suite at another deployment.
type stack struct {
	api      string // API_URL
	wiremock string // WIREMOCK_URL: the upstream's admin API, for faults and resets
	// feature:flags
	flagd string // FLAGD_URL: to confirm a flag change has been picked up
	// end feature:flags
}

func stackFromEnv() stack {
	get := func(name, def string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		return def
	}
	return stack{
		api:      get("API_URL", "http://localhost:8080"),
		wiremock: get("WIREMOCK_URL", "http://localhost:8081"),
		// feature:flags
		flagd: get("FLAGD_URL", "http://localhost:8016"),
		// end feature:flags
	}
}

func TestFeatures(t *testing.T) {
	s := stackFromEnv()
	preflight(t, s)
	// feature:flags
	if err := resetFlags(s); err != nil {
		t.Fatalf("resetting the test flags: %v\nIs the stack running with compose.integration.yaml? flagd must read integration/flagd.", err)
	}
	// end feature:flags

	o := opts
	o.TestingT = t
	suite := godog.TestSuite{
		Name:                "api",
		ScenarioInitializer: func(sc *godog.ScenarioContext) { initScenario(sc, s) },
		Options:             &o,
	}
	if suite.Run() != 0 {
		t.Fatal("feature tests failed")
	}
}

// preflight fails fast, with instructions, if the stack isn't running, rather
// than letting every scenario fail with a connection error.
func preflight(t *testing.T, s stack) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	for name, url := range map[string]string{
		"the API":  s.api + "/healthz",
		"WireMock": s.wiremock + "/__admin/health",
	} {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			status := fmt.Sprint(err)
			if err == nil {
				status = resp.Status
			}
			t.Fatalf("%s is not reachable at %s (%s).\nStart the stack first:\n"+
				"  docker compose -f compose.yaml -f compose.integration.yaml up -d --build --wait",
				name, url, status)
		}
	}
}
