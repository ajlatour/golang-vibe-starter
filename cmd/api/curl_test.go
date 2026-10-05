package main

import (
	"bufio"
	"crypto/sha256"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
)

// docs/curl.md shows engineers how to call the API with scripts/api and with
// plain curl. These tests run every command on that page, and the script
// itself, with the real bash and curl, against the real handlers on an
// in-process server. They need bash and curl, which macOS and CI's Ubuntu
// both have, and skip without them.

const guidePath = "../../docs/curl.md"

// needShell skips the test unless bash and curl are installed.
func needShell(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bash", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

// exchange is one request the API answered: its route pattern and status.
type exchange struct {
	route  string
	status int
}

// recorder wraps the API handler and records each exchange.
type recorder struct {
	next   http.Handler
	routes *http.ServeMux // the code's route patterns, to name each request's route

	mu   sync.Mutex
	seen []exchange
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, route := rec.routes.Handler(r)
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	rec.next.ServeHTTP(sw, r)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.seen = append(rec.seen, exchange{route, sw.status})
}

// take returns the exchanges recorded since the last call.
func (rec *recorder) take() []exchange {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	seen := rec.seen
	rec.seen = nil
	return seen
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// guideServer serves the API over an in-process store holding the items the
// WireMock stubs serve, so the guide's examples behave as they do against the
// local stack.
func guideServer(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	store := itemtest.NewMemStore()
	at := func(sec int) time.Time { return time.Date(2026, 1, 1, 0, 0, sec, 0, time.UTC) }
	store.Seed(
		item.Item{ID: "widget", Name: "Widget", Description: "A standard widget.", CreatedAt: at(0), UpdatedAt: at(0)},
		item.Item{ID: "gadget", Name: "Gadget", Description: "A gadget with extra features.", CreatedAt: at(1), UpdatedAt: at(1)},
		item.Item{ID: "gizmo", Name: "Gizmo", CreatedAt: at(2), UpdatedAt: at(2)},
	)
	routes := http.NewServeMux()
	for pattern := range codeRoutes(t) {
		routes.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}
	rec := &recorder{
		next:   newHandler(slog.New(slog.DiscardHandler), headerguard.DefaultPolicy(), composeAuth(t), noLimits, store),
		routes: routes,
	}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return srv, rec
}

// devKey is the local stack's development key, which docs/curl.md tells
// readers to use; composeAuth checks compose.yaml really configures it.
const devKey = "gvs_local-development-only"

// composeAuth is the authentication the compose stack runs with: the keys
// compose.yaml sets by default, so the guide's commands meet what they would
// locally.
func composeAuth(t *testing.T) apikey.Policy {
	t.Helper()
	b, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`API_KEYS: \$\{API_KEYS:-([^}]+)\}`).FindSubmatch(b)
	if m == nil {
		t.Fatal("compose.yaml doesn't set a default API_KEYS")
	}
	keys, err := apikey.ParseKeys(string(m[1]))
	if err != nil {
		t.Fatalf("compose.yaml's API_KEYS: %v", err)
	}
	if !slices.ContainsFunc(keys, func(k apikey.Key) bool { return k.Hash == sha256.Sum256([]byte(devKey)) }) {
		t.Fatalf("compose.yaml's API_KEYS doesn't include the development key %q the guide uses", devKey)
	}
	return apikey.Policy{Keys: keys, Exempt: []string{httpx.HealthzPath}}
}

// shell runs script with bash from the repository root, with API_URL set to
// apiURL, API_KEY to the development key (as docs/curl.md has readers export
// it) and CORRELATION_ID unset, and returns its combined output and exit
// code. extraEnv comes last, so it can override them.
func shell(t *testing.T, apiURL, script string, extraEnv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = "../.."
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "API_URL=") && !strings.HasPrefix(kv, "API_KEY=") && !strings.HasPrefix(kv, "CORRELATION_ID=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "API_URL="+apiURL, "API_KEY="+devKey)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("running bash: %v", err)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

// guideCommand is a command from docs/curl.md and the status the comment under
// it promises.
type guideCommand struct {
	line int
	text string
	want int
}

// callsAPI matches a command that calls the API, possibly after environment
// assignments or as the end of a pipeline.
var callsAPI = regexp.MustCompile(`(^|\|\s*)([A-Z_]+=\S+\s+)*(curl|scripts/api)\s`)

// guideCommands reads the commands in the guide's sh blocks. A command may
// span lines ending in a backslash. A comment right under it of the form
// "# 201 Created" is the status it must get; every command that calls the API
// needs one, and commands without one (such as an export) aren't run.
func guideCommands(t *testing.T) []guideCommand {
	t.Helper()
	f, err := os.Open(guidePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	statusRE := regexp.MustCompile(`^# (\d{3}) (.+)$`)

	var cmds []guideCommand
	var pending *guideCommand // the last command, until its status comment
	inBlock, continued := false, false
	finish := func() {
		if pending != nil && callsAPI.MatchString(pending.text) {
			t.Errorf("%s:%d: %q calls the API but has no \"# <status> <text>\" comment under it", guidePath, pending.line, pending.text)
		}
		pending = nil
	}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		switch {
		case !inBlock:
			inBlock = line == "```sh"
		case line == "```":
			finish()
			inBlock = false
		case continued:
			pending.text += " " + strings.TrimSpace(strings.TrimSuffix(line, `\`))
			continued = strings.HasSuffix(line, `\`)
		case strings.HasPrefix(line, "#"):
			if m := statusRE.FindStringSubmatch(line); m != nil && pending != nil {
				code, _ := strconv.Atoi(m[1])
				if m[2] != http.StatusText(code) {
					t.Errorf("%s:%d: %q: the text for %d is %q", guidePath, n, line, code, http.StatusText(code))
				}
				pending.want = code
				cmds = append(cmds, *pending)
				pending = nil
			} else {
				finish()
			}
		case strings.TrimSpace(line) == "":
			finish()
		default:
			finish()
			pending = &guideCommand{line: n, text: strings.TrimSpace(strings.TrimSuffix(line, `\`))}
			continued = strings.HasSuffix(line, `\`)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(cmds) == 0 {
		t.Fatalf("found no commands in %s; the page or this parser is broken", guidePath)
	}
	return cmds
}

// TestCurlGuide runs every command in docs/curl.md, in order, against one
// server, and checks each gets the status its comment promises and that,
// between them, they call every route the API serves.
func TestCurlGuide(t *testing.T) {
	needShell(t)
	srv, rec := guideServer(t)
	host := strings.TrimPrefix(srv.URL, "http://")

	called := map[string]bool{}
	for _, c := range guideCommands(t) {
		// The guide's curl commands name the local stack's address.
		script := strings.ReplaceAll(c.text, "localhost:8080", host)
		out, _ := shell(t, srv.URL, script)
		seen := rec.take()
		if len(seen) != 1 {
			t.Errorf("%s:%d: %s\nmade %d requests, want 1; output:\n%s", guidePath, c.line, c.text, len(seen), out)
			continue
		}
		if seen[0].status != c.want {
			t.Errorf("%s:%d: %s\ngot %d, the guide says %d; output:\n%s", guidePath, c.line, c.text, seen[0].status, c.want, out)
		}
		if seen[0].status < 300 {
			called[seen[0].route] = true
		}
	}
	for route := range codeRoutes(t) {
		if !called[route] {
			t.Errorf("no command in %s calls %s successfully; add one", guidePath, route)
		}
	}
}

// TestAPIScript checks what scripts/api promises in its usage comment.
func TestAPIScript(t *testing.T) {
	needShell(t)
	_, rec := guideServer(t)

	// The last request the script sent, as the API saw it.
	var mu sync.Mutex
	var lastReq *http.Request
	spy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastReq = r.Clone(r.Context())
		mu.Unlock()
		rec.ServeHTTP(w, r)
	}))
	t.Cleanup(spy.Close)

	bodyFile := filepath.Join(t.TempDir(), "item.json")
	if err := os.WriteFile(bodyFile, []byte(`{"name":"From a file"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		script   string
		env      []string
		wantExit int
		want     []string // in the output
	}{
		{name: "get", script: `scripts/api GET /items/widget`, want: []string{"HTTP/1.1 200 OK", "Client-Correlation-Id: ", `"name":"Widget"`}},
		{name: "lower-case method", script: `scripts/api get /items/widget`, want: []string{"HTTP/1.1 200 OK"}},
		{name: "inline body", script: `scripts/api POST /items '{"name":"Inline"}'`, want: []string{"HTTP/1.1 201 Created", "Location: /items/", `"name":"Inline"`}},
		{name: "body from a file", script: `scripts/api POST /items @` + bodyFile, want: []string{"HTTP/1.1 201 Created", `"name":"From a file"`}},
		{name: "body from stdin", script: `echo '{"name":"Piped"}' | scripts/api POST /items -`, want: []string{"HTTP/1.1 201 Created", `"name":"Piped"`}},
		{name: "a 4xx exits 1 and prints the body", script: `scripts/api GET /items/nope`, wantExit: 1, want: []string{"HTTP/1.1 404 Not Found", `{"error":"item not found"}`}},
		{name: "correlation ID", script: `scripts/api GET /items`, env: []string{"CORRELATION_ID=from-the-script"}, want: []string{"Client-Correlation-Id: from-the-script"}},
		{name: "curl options pass through", script: `scripts/api GET /items -H 'X-Extra: t0ken'`, want: []string{"HTTP/1.1 200 OK"}},
		{name: "a full URL", script: `scripts/api GET ` + spy.URL + `/healthz`, want: []string{`{"status":"ok"}`}},
		{name: "no arguments", script: `scripts/api`, wantExit: 2, want: []string{"scripts/api METHOD PATH [BODY]"}},
		{name: "a path without a slash", script: `scripts/api GET items`, wantExit: 2, want: []string{"PATH must start with /"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := shell(t, spy.URL, tt.script, tt.env...)
			if code != tt.wantExit {
				t.Errorf("exit %d, want %d; output:\n%s", code, tt.wantExit, out)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
		})
	}

	t.Run("a body is sent as JSON", func(t *testing.T) {
		shell(t, spy.URL, `scripts/api POST /items '{"name":"Typed"}'`)
		mu.Lock()
		defer mu.Unlock()
		if got := lastReq.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})
	t.Run("curl options reach the API", func(t *testing.T) {
		shell(t, spy.URL, `scripts/api GET /items -H 'X-Extra: t0ken'`)
		mu.Lock()
		defer mu.Unlock()
		if got := lastReq.Header.Get("X-Extra"); got != "t0ken" {
			t.Errorf("X-Extra = %q, want the one passed to curl", got)
		}
	})
	t.Run("API_KEY is sent", func(t *testing.T) {
		shell(t, spy.URL, `scripts/api GET /items`, "API_KEY=gvs_mine")
		mu.Lock()
		defer mu.Unlock()
		if got := lastReq.Header.Get("Authorization"); got != "Bearer gvs_mine" {
			t.Errorf("Authorization = %q, want Bearer gvs_mine", got)
		}
	})
	t.Run("the development key never goes elsewhere", func(t *testing.T) {
		shell(t, spy.URL, `scripts/api GET /healthz`, "API_KEY=")
		mu.Lock()
		defer mu.Unlock()
		if got := lastReq.Header.Get("Authorization"); got != "" {
			t.Errorf("with API_URL set and no API_KEY, Authorization = %q, want none", got)
		}
	})
	t.Run("piped output is the API's bytes", func(t *testing.T) {
		out, _ := shell(t, spy.URL, `scripts/api GET /items/widget 2>/dev/null`)
		if !strings.HasPrefix(out, `{"id":"widget",`) || strings.Contains(out, "\n  ") {
			t.Errorf("stdout = %q, want the compact JSON the API sent", out)
		}
	})
	t.Run("no API", func(t *testing.T) {
		out, code := shell(t, "http://127.0.0.1:1", `scripts/api GET /items`)
		if code == 0 || !strings.Contains(out, "Is the API running?") {
			t.Errorf("exit %d, output %q; want curl's failure and a hint", code, out)
		}
	})

	for _, e := range rec.take() {
		if e.status >= 500 {
			t.Errorf("the API answered %s with %d while the script ran", e.route, e.status)
		}
	}
}
