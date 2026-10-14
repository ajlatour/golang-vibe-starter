package main

import (
	"bufio"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestKnownConfigsStart starts the service with each configuration in
// testdata/configs: ones deployments run with (see its README). A release
// that would refuse one, or that no longer reads one of its settings (which
// so would be silently ignored, changing what the deployment does), fails
// here, before it reaches them. It also starts the service with every
// setting written out at the default .env.example documents, which must be
// a working configuration too.
func TestKnownConfigsStart(t *testing.T) {
	read := codeVariables(t)
	files, err := filepath.Glob("testdata/configs/*.env")
	if err != nil || len(files) == 0 {
		t.Fatalf("no configurations in testdata/configs: %v", err)
	}
	configs := map[string]map[string]string{}
	for _, f := range files {
		configs[filepath.Base(f)] = readEnvFile(t, f)
	}
	configs["the documented defaults"] = documentedDefaults(t)

	for name, env := range configs {
		t.Run(name, func(t *testing.T) {
			for k := range env {
				if !slices.Contains(read, k) {
					t.Errorf("sets %s, which the API no longer reads: the deployment's setting would be silently ignored", k)
				}
			}
			startWith(t, env)
		})
	}
}

// startWith runs the service with env, adjusted to run here: free local
// addresses, and a keys file read from testdata/configs. It fails the test
// unless the service starts and then stops cleanly.
func startWith(t *testing.T, env map[string]string) {
	t.Helper()
	env = maps.Clone(env)
	delete(env, "PORT")
	env["ADDR"] = "127.0.0.1:0"
	if env["PPROF_ADDR"] != "" {
		env["PPROF_ADDR"] = "127.0.0.1:0"
	}
	if f := env["API_KEYS_FILE"]; f != "" && !filepath.IsAbs(f) {
		env["API_KEYS_FILE"] = filepath.Join("testdata/configs", f)
	}
	if _, err := loadConfig(getenvFrom(env)); err != nil {
		t.Fatalf("refused: %v", err)
	}
	// Valid as given; now don't wait for a load balancer that isn't there,
	// and log JSON, which startApp reads the address from.
	delete(env, "SHUTDOWN_DELAY")
	env["LOG_FORMAT"] = "json"
	ctx, cancel := context.WithCancel(t.Context())
	_, _, errc := startApp(t, ctx, env)
	cancel()
	waitForShutdown(t, errc)
}

// readEnvFile reads KEY=VALUE lines, skipping blank lines and comments.
func readEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		k, v, ok := strings.Cut(text, "=")
		if !ok {
			t.Fatalf("%s:%d: not KEY=VALUE: %q", path, line, text)
		}
		env[k] = v
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return env
}

// documentedDefaults is every setting .env.example shows a default for,
// set to it, plus what's required: an upstream, and a key.
func documentedDefaults(t *testing.T) map[string]string {
	env := map[string]string{}
	for k, v := range exampleDefaults(t) {
		if v != "" {
			env[k] = v
		}
	}
	env["UPSTREAM_URL"] = "http://items.internal:8080"
	env["API_KEYS"] = "app:write:1f0d1e72ef1251a015cbec8ae3ad6880002ffe8ad4de82f4f496db34dc3e91dc"
	return env
}
