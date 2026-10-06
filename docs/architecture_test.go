// Package docs holds the project's documentation. Its only Go code is this
// test, which holds docs/architecture.md and docs/decisions.md to the code:
// the dependency rules, the files the requirements name, and the shape of
// the decision records.
package docs

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/ajlatour/golang-vibe-starter/"

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section returns the text of the Markdown section with the given heading,
// up to the next heading of the same or a higher level.
func section(t *testing.T, doc, heading string) string {
	t.Helper()
	i := strings.Index(doc, "\n"+heading+"\n")
	if i < 0 {
		t.Fatalf("no section %q", heading)
	}
	level := strings.Count(strings.Fields(heading)[0], "#")
	rest := doc[i+len(heading)+2:]
	for j := 0; j < len(rest); {
		k := strings.Index(rest[j:], "\n#")
		if k < 0 {
			break
		}
		j += k + 1
		hashes := len(rest[j:]) - len(strings.TrimLeft(rest[j:], "#"))
		if hashes <= level {
			return rest[:j]
		}
	}
	return rest
}

// TestServiceHasNoDependencies holds DEP-1: the service's module requires
// nothing.
func TestServiceHasNoDependencies(t *testing.T) {
	if regexp.MustCompile(`(?m)^require\b`).MatchString(read(t, "../go.mod")) {
		t.Error("go.mod requires a module: the service uses the standard library only (DEP-1, ADR-0002)")
	}
}

// TestToolingModulesAreSeparate holds DEP-2: what needs a dependency is a
// module of its own.
func TestToolingModulesAreSeparate(t *testing.T) {
	for _, dir := range []string{"../integration", "../perf/report"} {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			t.Errorf("%s has no go.mod of its own: its dependencies would become the service's (DEP-2)", dir)
		}
	}
}

// rule is one row of the package table.
type rule struct {
	kind    string
	any     bool // may import any runtime package
	imports []string
}

func packageTable(t *testing.T) map[string]rule {
	t.Helper()
	table := section(t, read(t, "architecture.md"), "### Packages and their dependencies")
	row := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([^|]+) \\| [^|]+ \\| ([^|]+) \\|$")
	code := regexp.MustCompile("`([^`]+)`")
	rules := map[string]rule{}
	for _, m := range row.FindAllStringSubmatch(table, -1) {
		r := rule{kind: strings.TrimSpace(m[2])}
		switch may := strings.TrimSpace(m[3]); may {
		case "any runtime package":
			r.any = true
		case "nothing":
		default:
			for _, c := range code.FindAllStringSubmatch(may, -1) {
				r.imports = append(r.imports, c[1])
			}
		}
		rules[m[1]] = r
	}
	if len(rules) == 0 {
		t.Fatal("found no rows in the package table")
	}
	return rules
}

// codeImports returns, for every package under cmd/, internal/ and flagd/,
// the module's packages its non-test files import.
func codeImports(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, root := range []string{"../cmd", "../internal", "../flagd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			pkg := filepath.ToSlash(strings.TrimPrefix(filepath.Dir(path), "../"))
			if _, ok := out[pkg]; !ok {
				out[pkg] = nil
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if dep, ok := strings.CutPrefix(p, module); ok && !slices.Contains(out[pkg], dep) {
					out[pkg] = append(out[pkg], dep)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// TestPackageDependencies holds DEP-3: every package imports only what the
// table in docs/architecture.md allows, the table allows nothing a package no
// longer imports (so it reads as the dependencies there are), the table lists
// exactly the packages there are, and only tests import test support.
func TestPackageDependencies(t *testing.T) {
	rules, code := packageTable(t), codeImports(t)
	for pkg := range rules {
		if _, ok := code[pkg]; !ok {
			t.Errorf("the package table lists %s, which doesn't exist: remove its row", pkg)
		}
	}
	for pkg, deps := range code {
		r, ok := rules[pkg]
		if !ok {
			t.Errorf("%s isn't in the package table in docs/architecture.md: add it, with what it may import", pkg)
			continue
		}
		for _, dep := range deps {
			testSupport := rules[dep].kind == "test support"
			switch {
			case testSupport && r.kind != "test support":
				t.Errorf("%s imports %s, which is test support: only tests may", pkg, dep)
			case r.any && !testSupport:
			case !slices.Contains(r.imports, dep):
				t.Errorf("%s imports %s, which the package table doesn't allow: change the code, or the table and so the architecture", pkg, dep)
			}
		}
		for _, allowed := range r.imports {
			if !slices.Contains(deps, allowed) {
				t.Errorf("the package table lets %s import %s, which it doesn't: remove it from the table", pkg, allowed)
			}
		}
	}
}

// TestDocumentedPathsExist checks every repository path the two documents
// name, as a requirement's verification or otherwise, exists.
func TestDocumentedPathsExist(t *testing.T) {
	entries, err := os.ReadDir("..")
	if err != nil {
		t.Fatal(err)
	}
	top := map[string]bool{}
	for _, e := range entries {
		top[e.Name()] = true
	}
	code := regexp.MustCompile("`([^`\\s]+)`")
	checked := 0
	for _, doc := range []string{"architecture.md", "decisions.md"} {
		for _, m := range code.FindAllStringSubmatch(read(t, doc), -1) {
			p := m[1]
			first, _, nested := strings.Cut(p, "/")
			// A path in this repository: under one of its top-level entries,
			// or a file at its root.
			if !top[first] && (nested || !regexp.MustCompile(`\.(go|mod|json|ya?ml|md|sh)$`).MatchString(p)) {
				continue
			}
			checked++
			if _, err := os.Stat(filepath.Join("..", p)); err != nil {
				t.Errorf("%s names %s, which doesn't exist", doc, p)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("checked only %d paths; the documents or this test have changed shape", checked)
	}
}

var (
	recordRE = regexp.MustCompile(`(?m)^## (ADR-(\d{4})): (.+)$`)
	statusRE = regexp.MustCompile(`^(Proposed|Accepted|Superseded by (ADR-\d{4}))$`)
)

// TestDecisionRecords checks each record in docs/decisions.md is complete,
// they're numbered in order, the index matches them, and every ADR either
// document cites exists.
func TestDecisionRecords(t *testing.T) {
	doc := read(t, "decisions.md")
	heads := recordRE.FindAllStringSubmatchIndex(doc, -1)
	if len(heads) == 0 {
		t.Fatal("no records")
	}
	ids := map[string]bool{}
	index := section(t, doc, "## Index")
	for i, h := range heads {
		id, num, title := doc[h[2]:h[3]], doc[h[4]:h[5]], doc[h[6]:h[7]]
		ids[id] = true
		if n, _ := strconv.Atoi(num); n != i+1 {
			t.Errorf("%s is record %d: number them in order, with no gaps", id, i+1)
		}
		end := len(doc)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		body := doc[h[1]:end]
		status := regexp.MustCompile(`(?m)^- \*\*Status:\*\* (.+)$`).FindStringSubmatch(body)
		if status == nil || !statusRE.MatchString(status[1]) {
			t.Errorf("%s: want a status line, Proposed, Accepted or Superseded by ADR-NNNN", id)
		}
		if !regexp.MustCompile(`(?m)^- \*\*Date:\*\* \d{4}-\d{2}-\d{2}`).MatchString(body) {
			t.Errorf("%s: want a date line, YYYY-MM-DD", id)
		}
		for _, part := range []string{"**Context.**", "**Decision.**", "**Consequences.**"} {
			if !strings.Contains(body, "\n"+part+" ") {
				t.Errorf("%s: no %s paragraph", id, part)
			}
		}
		row := regexp.MustCompile(`(?m)^\| \[` + id + `\]\(#[^)]+\) \| (.+) \| (.+) \|$`).FindStringSubmatch(index)
		switch {
		case row == nil:
			t.Errorf("%s isn't in the index", id)
		case row[1] != title:
			t.Errorf("%s: the index says %q, the record %q", id, row[1], title)
		case status != nil && row[2] != status[1]:
			t.Errorf("%s: the index says %s, the record %s", id, row[2], status[1])
		}
	}
	for _, d := range []string{"decisions.md", "architecture.md"} {
		for _, cited := range regexp.MustCompile(`ADR-\d{4}`).FindAllString(read(t, d), -1) {
			if cited != "ADR-NNNN" && !ids[cited] {
				t.Errorf("%s cites %s, which doesn't exist", d, cited)
			}
		}
	}
}

// TestRequirementIDsAreUnique checks no two requirements share an ID.
func TestRequirementIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\| ([A-Z]+-\d+) \|`).FindAllStringSubmatch(read(t, "architecture.md"), -1) {
		if seen[m[1]] {
			t.Errorf("two requirements are %s", m[1])
		}
		seen[m[1]] = true
	}
	if len(seen) < 10 {
		t.Fatalf("found %d requirements; the table or this test have changed shape", len(seen))
	}
}
