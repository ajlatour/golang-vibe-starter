package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo makes a git repository in a temporary directory holding files
// (path to content), all added, and returns it.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestInit runs the tool on a small repository shaped like the template:
// what it rewrites, what it leaves, and the history it starts.
func TestInit(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"go.mod":                     "module github.com/ajlatour/golang-vibe-starter\n\ngo 1.27.2\n",
		"cmd/api/main.go":            "package main\n\nimport _ \"github.com/ajlatour/golang-vibe-starter/internal/x\"\n",
		"compose.yaml":               "name: golang-vibe-starter\nimage: golang-vibe-starter-api\n",
		".github/workflows/soak.yml": "if: github.repository == 'ajlatour/golang-vibe-starter'\n",
		".github/CODEOWNERS":         "* @ajlatour\n",
		"LICENSE":                    "Copyright (c) 2026 AJ Latour\n",
		"CHANGELOG.md":               "# Changelog\n\nAbout golang-vibe-starter.\n\n## Unreleased\n\n## [0.6.1] - 2026-10-14\n\n- Things.\n",
		"changes/README.md":          "# Changes\n",
		"changes/a-fix.md":           "### Fixed\n\n- A fix.\n",
		"docs/openapi.json":          "{\n  \"info\": {\n    \"title\": \"golang-vibe-starter API\",\n    \"version\": \"0.6.1\"\n  }\n}\n",
		"docs/audit-2026-10.md":      "# Audit\n",
		"docs/site/build.sh":         "navigation='Start|README.md|index.html\nArchitecture|docs/decisions.md|decisions.html\nArchitecture|docs/audit-2026-10.md|audit-2026-10.html'\n",
		"README.md":                  "# golang-vibe-starter\n\n- [Architecture decisions](docs/decisions.md): why.\n- [Audit, October 2026](docs/audit-2026-10.md): a review of the whole codebase, what it found,\n  and what was done about it.\n\n## Layout\n",
		"internal/x/logo.bin":        "\x00golang-vibe-starter",
	})
	var out strings.Builder
	if err := run([]string{"-name", "orders", "-module", "github.com/acme/orders"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"go.mod":                     "module github.com/acme/orders\n",
		"cmd/api/main.go":            `"github.com/acme/orders/internal/x"`,
		"compose.yaml":               "name: orders\nimage: orders-api\n",
		".github/workflows/soak.yml": "github.repository == 'acme/orders'",
		".github/CODEOWNERS":         "* @acme\n",
		"docs/openapi.json":          `"title": "orders API",` + "\n    \"version\": \"0.0.0\"",
		"docs/site/build.sh":         "decisions.html'\n",
		"README.md":                  "# orders\n\n- [Architecture decisions](docs/decisions.md): why.\n\n## Layout\n",
	} {
		if got := read(t, filepath.Join(dir, path)); !strings.Contains(got, want) {
			t.Errorf("%s:\n%s\nwant it to contain\n%s", path, got, want)
		}
	}
	if got := read(t, filepath.Join(dir, "LICENSE")); !strings.Contains(got, "AJ Latour") {
		t.Errorf("LICENSE changed: %s", got)
	}
	if got := read(t, filepath.Join(dir, "internal/x/logo.bin")); !strings.Contains(got, "golang-vibe-starter") {
		t.Error("a binary file was rewritten")
	}
	changelog := read(t, filepath.Join(dir, "CHANGELOG.md"))
	for _, want := range []string{"About orders.", "## Unreleased\n\n## [0.0.0] - ", "Started from the [golang-vibe-starter](https://github.com/ajlatour/golang-vibe-starter) template, v0.6.1."} {
		if !strings.Contains(changelog, want) {
			t.Errorf("CHANGELOG.md lacks %q:\n%s", want, changelog)
		}
	}
	if strings.Contains(changelog, "0.6.1]") {
		t.Errorf("CHANGELOG.md kept the template's releases:\n%s", changelog)
	}
	for _, gone := range []string{"changes/a-fix.md", "docs/audit-2026-10.md"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "changes/README.md")); err != nil {
		t.Error("changes/README.md went too")
	}
	if !strings.Contains(out.String(), "LICENSE: set the copyright holder") {
		t.Errorf("no word on what's left to do:\n%s", out.String())
	}
}

func TestInitOffGitHub(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"go.mod":             "module github.com/ajlatour/golang-vibe-starter\n",
		".github/CODEOWNERS": "* @ajlatour\n",
		"CHANGELOG.md":       "# Changelog\n\n## Unreleased\n\n## [0.6.1] - 2026-10-14\n",
		"docs/openapi.json":  `{"info": {"version": "0.6.1"}}`,
		"docs/site/build.sh": "navigation=''\n",
		"README.md":          "# golang-vibe-starter\n",
	})
	var out strings.Builder
	if err := run([]string{"-name", "billing", "-module", "git.example.com/payments/billing"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, ".github/CODEOWNERS")); got != "# * @your-team\n" {
		t.Errorf("CODEOWNERS = %q, want the template's owner left out", got)
	}
	if !strings.Contains(out.String(), "CODEOWNERS: name who reviews what") {
		t.Errorf("no word about CODEOWNERS:\n%s", out.String())
	}
}

func TestInitRefuses(t *testing.T) {
	dir := gitRepo(t, map[string]string{"CHANGELOG.md": "## Unreleased\n\n## [0.6.1] - 2026-10-14\n"})
	for name, args := range map[string][]string{
		"no name":               {"-module", "github.com/acme/orders"},
		"a name with capitals":  {"-name", "Orders", "-module", "github.com/acme/orders"},
		"a name with a space":   {"-name", "my orders", "-module", "github.com/acme/orders"},
		"no module":             {"-name", "orders"},
		"the template's module": {"-name", "orders", "-module", "github.com/ajlatour/golang-vibe-starter"},
	} {
		if err := run(args, dir, io.Discard); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// Already initialized: the changelog's only release is 0.0.0, which
	// it can run on again, but a repository with no release at all isn't
	// the template.
	bare := gitRepo(t, map[string]string{"CHANGELOG.md": "## Unreleased\n"})
	if err := run([]string{"-name", "orders", "-module", "github.com/acme/orders"}, bare, io.Discard); err == nil {
		t.Error("a repository with no release: no error")
	}
}

// TestInitOnACopy initializes a copy of this repository, then builds, vets
// and tests every module of the result, and checks nothing still names the
// template, so make init can't quietly fall behind the code it rewrites. It
// takes a minute: make init-check runs it (TEMPLATE_CHECK=1), as CI's template job
// does.
func TestInitOnACopy(t *testing.T) {
	if os.Getenv("TEMPLATE_CHECK") == "" {
		t.Skip("TEMPLATE_CHECK is not set; make init-check runs this")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	list, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for f := range strings.SplitSeq(strings.TrimRight(string(list), "\x00"), "\x00") {
		info, err := os.Stat(filepath.Join(root, f))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, info.Mode().Perm()); err != nil { // scripts stay executable
			t.Fatal(err)
		}
	}
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")

	var out strings.Builder
	if err := run([]string{"-name", "orders", "-module", "github.com/acme/orders"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	t.Log(out.String())

	// Nothing names the template any more, but the changelog's line saying
	// where it came from, the license's holder, and this tool.
	grep := exec.Command("git", "grep", "-n", "-I", "-e", "golang-vibe-starter", "-e", "ajlatour", "--", ".", ":!CHANGELOG.md", ":!LICENSE", ":!cmd/initservice")
	grep.Dir = dir
	if left, _ := grep.Output(); len(left) > 0 {
		t.Errorf("still naming the template:\n%s", left)
	}

	goCmd := func(module string, args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = filepath.Join(dir, module)
		// Not this test again, on the copy's own copy.
		cmd.Env = append(os.Environ(), "TEMPLATE_CHECK=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: go %s: %v\n%s", module, strings.Join(args, " "), err, out)
		}
	}
	for _, module := range []string{".", "integration", "perf/report"} {
		goCmd(module, "vet", "./...")
	}
	goCmd("tools", "build", "-o", t.TempDir(), "tool") // tool directives, no packages of its own
	goCmd(".", "test", "-count=1", "./...")
}
