// Command newresource adds a resource to the API: `make resource NAME=order`,
// or with PLURAL=… for a plural that isn't NAME with an s. See
// docs/development.md#adding-a-resource.
//
// It copies everything the example resource, /items, owns, renamed: the
// package (internal/item: the model, the store interface, the handlers, the
// stores in memory and on the upstream, the store contract test), its wiring
// in cmd/api and the spec tests' view of it, its WireMock stubs and its
// integration feature. And it adds the new resource to every list that
// names each one: the API's resources, the spec's paths and schemas, the
// feature flags, the load test's traffic mix, the curl guide and the
// architecture's package table. So the new resource builds, and passes every
// check, as generated; what's left is to make it the resource it's meant to
// be, which it lists.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ajlatour/golang-vibe-starter/internal/rename"
)

// The example resource, which every new one is copied from.
const (
	fromName   = "item"
	fromPlural = "items"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,30}$`)

func main() {
	if err := run(os.Args[1:], ".", os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "resource:", err)
		os.Exit(1)
	}
}

func run(args []string, dir string, out io.Writer) error {
	fs := flag.NewFlagSet("newresource", flag.ContinueOnError)
	name := fs.String("name", "", "the resource's name, singular, in lower-case letters and digits: order")
	plural := fs.String("plural", "", "its plural, if it isn't the name with an s: categories")
	from := fs.String("from", fromName, "the resource to copy, singular")
	fromPl := fs.String("from-plural", "", "its plural, if it isn't the name with an s")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *fromPl != "":
	case *from == fromName:
		*fromPl = fromPlural
	default:
		*fromPl = *from + "s"
	}
	if *plural == "" {
		*plural = *name + "s"
	}
	for _, n := range []string{*name, *plural} {
		if !nameRE.MatchString(n) {
			return fmt.Errorf("%q: want lower-case letters and digits, starting with a letter: it names a Go package and the resource's path", n)
		}
	}
	if *name == *plural {
		return fmt.Errorf("the name and its plural are both %q: give a plural with PLURAL", *name)
	}
	if strings.Contains(*name, *from) || strings.Contains(*plural, *from) {
		return fmt.Errorf("%q: a name containing %q would be renamed along with the example's", *name, *from)
	}
	// lit maps the names this file's text is written with, the example's, to
	// the resource copied; r, that one's to the new one's.
	lit := rename.Renamer{From: fromName, FromPlural: fromPlural, To: *from, ToPlural: *fromPl}
	gomod, err := readFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	keep := []string{rename.Module(string(gomod))}
	lit.Keep = keep
	r := rename.Renamer{From: *from, FromPlural: *fromPl, To: *name, ToPlural: *plural, Keep: keep}
	if _, err := os.Stat(filepath.Join(dir, "internal", *name)); err == nil {
		return fmt.Errorf("internal/%s already exists", *name)
	}

	files, err := owned(dir, *from, *fromPl)
	if err != nil {
		return err
	}
	for _, f := range files {
		b, err := readFile(filepath.Join(dir, f))
		if err != nil {
			return err
		}
		to := filepath.Join(dir, r.Text(f))
		if _, err := os.Stat(to); err == nil {
			return fmt.Errorf("%s already exists", r.Text(f))
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil { // #nosec G301 -- a source directory, as checked in
			return err
		}
		if err := writeFile(to, []byte(r.Text(string(b)))); err != nil {
			return err
		}
	}
	for _, e := range edits {
		if _, err := os.Stat(filepath.Join(dir, e.file)); errors.Is(err, os.ErrNotExist) && optional[e.file] {
			continue // a part this service was started without
		}
		if err := edit(dir, e.file, func(s string) (string, error) { return e.fn(s, r, lit) }); err != nil {
			return fmt.Errorf("%s: %w", e.file, err)
		}
	}

	fmt.Fprintf(out, "Added /%s, copied from /%s: %d files, and an entry in each list of resources.\n\n", *plural, *fromPl, len(files))
	name2 := func(s string) string { return r.Text(lit.Text(s)) }
	fmt.Fprintln(out, "It builds and passes every check as it is. To make it the resource you mean:")
	fmt.Fprintf(out, "  - internal/%s/%s.go: its fields, each a value object enforcing its rules, in %s and Draft.\n", *name, *name, name2("Item"))
	fmt.Fprintf(out, "  - internal/%s/handlers.go: Request and Response, the shapes the API speaks.\n", *name)
	fmt.Fprintf(out, "  - docs/openapi.json: %s, %s and %s, to match; the tests say where they differ.\n", name2("Item"), name2("ItemInput"), name2("ItemList"))
	fmt.Fprintf(out, "  - Its store: internal/%s/httpstore speaks to the upstream's /%s; replace it with the store you need.\n", *name, *plural)
	if _, err := os.Stat(filepath.Join(dir, "flagd")); err == nil {
		fmt.Fprintf(out, "  - Its flag, %s: keep it, or remove it from the handlers, flagd/flags.json and docs/feature-flags.md.\n", name2("items-list-total"))
	}
	fmt.Fprintf(out, "  - The examples: cmd/api/%s_test.go's scenarios, wiremock/mappings/%s-*.json, docs/curl.md, the load test's mix if there is one, integration/features/%s.feature.\n", *plural, *plural, *plural)
	fmt.Fprintln(out, "  - README.md and the docs, where they describe the API's resources.")
	fmt.Fprintln(out, "  - Run make check, make lint and make docs-check, and commit.")
	return nil
}

// owned lists the files the resource named name (plural) owns, which are
// copied: its package, and each file named for it.
func owned(dir, name, plural string) ([]string, error) {
	// #nosec G204 -- git, with paths built from names nameRE checked (or the example's)
	cmd := exec.Command("git", "ls-files", "-z", "internal/"+name,
		"cmd/api/"+plural+".go", "cmd/api/"+plural+"_test.go",
		"wiremock/mappings/"+plural+"-*.json", "integration/features/"+plural+".feature")
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing the files under version control (run it in a git checkout): %w", err)
	}
	var files []string
	for f := range strings.SplitSeq(strings.TrimRight(string(b), "\x00"), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no internal/%s: the resource to copy from isn't there", name)
	}
	return files, nil
}

// optional are the files edits names that a service may not have: those of
// the template's optional parts.
var optional = map[string]bool{
	"perf/k6.js": true, "flagd/flags.json": true, "integration/flagd/flags.json": true, "docs/feature-flags.md": true,
}

// edits add the new resource to each list that names every resource.
var edits = []struct {
	file string
	// fn adds it: r renames the copied resource to the new one, and lit the
	// example's names, which the literals here are written with, to the
	// copied one's.
	fn func(s string, r, lit rename.Renamer) (string, error)
}{
	{"cmd/api/resources.go", func(s string, r, lit rename.Renamer) (string, error) {
		return appendToList(s, "var resources = []resource{\n", "\t"+r.ToPlural+"Resource,\n")
	}},
	{"cmd/api/resources_test.go", func(s string, r, lit rename.Renamer) (string, error) {
		return appendToList(s, "var testResources = []testResource{\n", "\t"+r.ToPlural+"Test,\n")
	}},
	{"docs/openapi.json", func(s string, r, lit rename.Renamer) (string, error) {
		return copyMembers(s, r, lits(lit, `"/items": {`, `"/items/{id}": {`), lits(lit, `"ItemID": {`),
			lits(lit, `"Item": {`, `"ItemInput": {`, `"ItemList": {`))
	}},
	{"flagd/flags.json", func(s string, r, lit rename.Renamer) (string, error) {
		return copyMembers(s, r, lits(lit, `"items-list-total": {`))
	}},
	{"integration/flagd/flags.json", func(s string, r, lit rename.Renamer) (string, error) {
		return copyMembers(s, r, lits(lit, `"items-list-total": {`))
	}},
	{"docs/feature-flags.md", func(s string, r, lit rename.Renamer) (string, error) {
		return copyLines(s, r, regexp.MustCompile("^\\| `"+lit.Text("items-list-total")+"` \\|"))
	}},
	{"perf/k6.js", func(s string, r, lit rename.Renamer) (string, error) {
		return copyLines(s, r, regexp.MustCompile(`^  \{ weight: .*route: '[A-Z]+ /`+lit.ToPlural+`[/'{]`))
	}},
	{"docs/architecture.md", func(s string, r, lit rename.Renamer) (string, error) {
		return copyLines(s, r, rowRE(lit.To))
	}},
	{"docs/curl.md", func(s string, r, lit rename.Renamer) (string, error) {
		const before = "\n## Plain curl\n"
		i := strings.Index(s, before)
		if i < 0 {
			return "", errors.New("no \"## Plain curl\" section to put the new resource's examples before")
		}
		return s[:i] + r.Text(lit.Text(curlSection)) + s[i:], nil
	}},
}

// rowRE matches the package table's rows for the packages of the resource
// called name.
func rowRE(name string) *regexp.Regexp {
	return regexp.MustCompile("^\\| `internal/" + regexp.QuoteMeta(name) + "(/[a-z]+)?` \\|")
}

// lits is opens, written with the example's names, with the copied
// resource's.
func lits(lit rename.Renamer, opens ...string) []string {
	out := make([]string, len(opens))
	for i, o := range opens {
		out[i] = lit.Text(o)
	}
	return out
}

// curlSection is the new resource's examples in docs/curl.md, renamed; the
// WireMock stubs, copied, hold the same three.
const curlSection = `
### Items

` + "```sh" + `
scripts/api GET /items
# 200 OK
scripts/api GET /items/widget
# 200 OK
scripts/api POST /items '{"name": "Sprocket", "description": "Spins."}'
# 201 Created
scripts/api PUT /items/widget '{"name": "Widget", "description": "Now in blue."}'
# 200 OK
scripts/api DELETE /items/gizmo
# 204 No Content
` + "```" + `
`

// appendToList adds entry at the end of the list that opens with open.
func appendToList(s, open, entry string) (string, error) {
	i := strings.Index(s, open)
	if i < 0 {
		return "", fmt.Errorf("no %q", strings.TrimSpace(open))
	}
	end := strings.Index(s[i:], "\n}")
	if end < 0 {
		return "", fmt.Errorf("%q isn't closed", strings.TrimSpace(open))
	}
	end += i + 1
	return s[:end] + entry + s[end:], nil
}

// copyLines adds, after each line that matches re, its copy renamed.
func copyLines(s string, r rename.Renamer, re *regexp.Regexp) (string, error) {
	lines := strings.SplitAfter(s, "\n")
	var b strings.Builder
	n := 0
	for i, l := range lines {
		b.WriteString(l)
		if re.MatchString(l) {
			n++
			// The copies follow the last of a run of such lines, in its
			// order: the k6 mix's, or the package table's rows.
			if i+1 < len(lines) && re.MatchString(lines[i+1]) {
				continue
			}
			start := i
			for start > 0 && re.MatchString(lines[start-1]) {
				start--
			}
			for _, c := range lines[start : i+1] {
				b.WriteString(r.Text(c))
			}
		}
	}
	if n == 0 {
		return "", fmt.Errorf("no line matching %s", re)
	}
	return b.String(), nil
}

// copyMembers adds, after each group of JSON object members, the group's
// copy renamed. A group is members side by side, each named by how it opens
// (`"key": {`), found once in s. It works on the text, so the file keeps its
// layout.
func copyMembers(s string, r rename.Renamer, groups ...[]string) (string, error) {
	for _, group := range groups {
		start, end := -1, -1
		for _, open := range group {
			if n := strings.Count(s, open); n != 1 {
				return "", fmt.Errorf("%s: want it once, found it %d times", open, n)
			}
			i := strings.Index(s, open)
			close, err := closingBrace(s, i+len(open)-1)
			if err != nil {
				return "", fmt.Errorf("%s: %w", open, err)
			}
			if start < 0 {
				start = i
			} else if strings.Trim(s[end+1:i], " \t\n,") != "" {
				return "", fmt.Errorf("%s doesn't follow %s", open, group[0])
			}
			end = close
		}
		indent := s[strings.LastIndexByte(s[:start], '\n')+1 : start]
		s = s[:end+1] + ",\n" + indent + r.Text(s[start:end+1]) + s[end+1:]
	}
	return s, nil
}

// closingBrace is the index of the brace closing the one at s[open],
// skipping those in strings.
func closingBrace(s string, open int) (int, error) {
	depth, inString := 0, false
	for i := open; i < len(s); i++ {
		c := s[i]
		switch {
		case inString && c == '\\':
			i++
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, errors.New("not closed")
}

func edit(dir, file string, fn func(string) (string, error)) error {
	path := filepath.Join(dir, file)
	b, err := readFile(path)
	if err != nil {
		return err
	}
	s, err := fn(string(b))
	if err != nil {
		return err
	}
	return writeFile(path, []byte(s))
}

// readFile and writeFile read and write the files of the repository the
// resource is added to: the one its user named, in which they run it.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 -- a file in the repository being changed
}

// writeFile keeps an existing file's mode.
func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o644) // #nosec G306 G703 -- a file in the repository being changed, a source file as checked in
}
