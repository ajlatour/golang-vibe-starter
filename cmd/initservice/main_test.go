package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// TestInitOnACopy initializes copies of this repository, with every
// feature, each left out on its own, and all of them left out, then builds,
// vets and tests every module of each result, and checks nothing names the
// template, a feature left out, or a marker, so make init can't quietly fall
// behind the code it rewrites. It takes a few minutes: make init-check runs
// it (TEMPLATE_CHECK=1), as CI's template job does.
func TestInitOnACopy(t *testing.T) {
	if os.Getenv("TEMPLATE_CHECK") == "" {
		t.Skip("TEMPLATE_CHECK is not set; make init-check runs this")
	}
	cases := [][]feature{nil}
	var all []feature
	for _, f := range features {
		cases = append(cases, []feature{f})
		all = append(all, f)
	}
	if len(features) > 1 {
		cases = append(cases, all)
	}
	for _, without := range cases {
		var names []string
		for _, f := range without {
			names = append(names, f.Name)
		}
		label := "everything"
		if len(names) > 0 {
			label = "without " + strings.Join(names, ",")
		}
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			checkInit(t, without, strings.Join(names, ","))
		})
	}
}

func checkInit(t *testing.T, without []feature, list string) {
	dir := copyRepo(t)
	var out strings.Builder
	if err := run([]string{"-name", "orders", "-module", "github.com/acme/orders", "-without", list}, dir, &out); err != nil {
		t.Fatal(err)
	}
	t.Log(out.String())

	grep := func(what string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"grep", "-n", "-I"}, args...)...)
		cmd.Dir = dir
		if left, _ := cmd.Output(); len(left) > 0 {
			t.Errorf("%s:\n%s", what, left)
		}
	}
	// Nothing names the template any more, but the changelog's and the
	// roadmap's line saying where it came from, and the license's holder.
	grep("still naming the template", "-e", "golang-vibe-starter", "-e", "ajlatour", "--", ".", ":!CHANGELOG.md", ":!ROADMAP.md", ":!LICENSE")
	grep("a feature marker left in", "-E", "-e", markerRE.String(), "--", ".")
	for _, f := range append(slices.Clip(without), initFeature) {
		for _, word := range f.gone {
			// The decision records are history, and the changelog's line
			// says where the service came from.
			// cmd/newresource knows the parts a service may not have.
			grep("still naming "+f.Name+", left out", "-F", "-e", word, "--", ".", ":!docs/adr", ":!CHANGELOG.md", ":!cmd/newresource")
		}
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
		if _, err := os.Stat(filepath.Join(dir, module, "go.mod")); err == nil { // perf/report goes with perf
			goCmd(module, "vet", "./...")
		}
	}
	goCmd("tools", "build", "-o", t.TempDir(), "tool") // tool directives, no packages of its own
	goCmd(".", "test", "-count=1", "./...")
	if gofmt, err := exec.Command("gofmt", "-l", dir).Output(); err != nil || len(gofmt) > 0 {
		t.Errorf("not gofmt'd: %s %v", gofmt, err)
	}
}

// copyRepo copies the files under version control, as they are in the
// working tree, new ones included, to a new git repository, and returns it.
func copyRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
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
	return dir
}
