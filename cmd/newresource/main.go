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
	if err := fs.Parse(args); err != nil {
		return err
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
	if strings.Contains(*name, fromName) || strings.Contains(*plural, fromName) {
		return fmt.Errorf("%q: a name containing %q would be renamed along with the example's", *name, fromName)
	}
	r := renamer{to: *name, toPlural: *plural}
	if _, err := os.Stat(filepath.Join(dir, "internal", *name)); err == nil {
		return fmt.Errorf("internal/%s already exists", *name)
	}

	files, err := owned(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		b, err := readFile(filepath.Join(dir, f))
		if err != nil {
			return err
		}
		to := filepath.Join(dir, r.text(f))
		if _, err := os.Stat(to); err == nil {
			return fmt.Errorf("%s already exists", r.text(f))
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil { // #nosec G301 -- a source directory, as checked in
			return err
		}
		if err := writeFile(to, []byte(r.text(string(b)))); err != nil {
			return err
		}
	}
	for _, e := range edits {
		if err := edit(dir, e.file, func(s string) (string, error) { return e.fn(s, r) }); err != nil {
			return fmt.Errorf("%s: %w", e.file, err)
		}
	}

	fmt.Fprintf(out, "Added /%s, copied from /%s: %d files, and an entry in each list of resources.\n\n", *plural, fromPlural, len(files))
	fmt.Fprintln(out, "It builds and passes every check as it is. To make it the resource you mean:")
	fmt.Fprintf(out, "  - internal/%s/%s.go: its fields, each a value object enforcing its rules, in %s and Draft.\n", *name, *name, r.text("Item"))
	fmt.Fprintf(out, "  - internal/%s/handlers.go: Request and Response, the shapes the API speaks.\n", *name)
	fmt.Fprintf(out, "  - docs/openapi.json: %s, %s and %s, to match; the tests say where they differ.\n", r.text("Item"), r.text("ItemInput"), r.text("ItemList"))
	fmt.Fprintf(out, "  - Its store: internal/%s/httpstore speaks to the upstream's /%s; replace it with the store you need.\n", *name, *plural)
	fmt.Fprintf(out, "  - Its flag, %s: keep it, or remove it from the handlers, flagd/flags.json and docs/feature-flags.md.\n", r.text("items-list-total"))
	fmt.Fprintf(out, "  - The examples: cmd/api/%s_test.go's scenarios, wiremock/mappings/%s-*.json, docs/curl.md, perf/k6.js, integration/features/%s.feature.\n", *plural, *plural, *plural)
	fmt.Fprintln(out, "  - README.md and the docs, where they describe the API's resources.")
	fmt.Fprintln(out, "  - Run make check, make lint and make docs-check, and commit.")
	return nil
}

// owned lists the files the example resource owns, which are copied: its
// package, and each file named for it.
func owned(dir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "internal/"+fromName,
		"cmd/api/"+fromPlural+".go", "cmd/api/"+fromPlural+"_test.go",
		"wiremock/mappings/"+fromPlural+"-*.json", "integration/features/"+fromPlural+".feature")
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
		return nil, fmt.Errorf("no internal/%s: the example resource, which new ones are copied from, has gone", fromName)
	}
	return files, nil
}

// renamer renames the example resource's names to a new one's, keeping
// their case: item to order, Items to Orders, ITEM to ORDER. It leaves the
// names that aren't the resource's: "items", the list's key in every
// resource's JSON, and Items, the field holding it (httpx.List).
type renamer struct{ to, toPlural string }

var wordRE = regexp.MustCompile(`(?i)` + fromName + `(s)?`)

func (r renamer) text(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range wordRE.FindAllStringSubmatchIndex(s, -1) {
		i, j := m[0], m[1]
		word := s[i:j]
		plural := m[2] >= 0
		var prev, next byte
		if i > 0 {
			prev = s[i-1]
		}
		if j < len(s) {
			next = s[j]
		}
		if r.keep(s, word, plural, prev, next, j) {
			continue
		}
		b.WriteString(s[last:i])
		repl := r.to
		if plural {
			repl = r.toPlural
		}
		switch {
		case word == strings.ToUpper(word):
			repl = strings.ToUpper(repl)
		case word[0] >= 'A' && word[0] <= 'Z':
			repl = strings.ToUpper(repl[:1]) + repl[1:]
		}
		article(&b, repl)
		b.WriteString(repl)
		last = j
	}
	b.WriteString(s[last:])
	return b.String()
}

// keep reports whether word, at s[:j], isn't the resource's name.
func (r renamer) keep(s, word string, plural bool, prev, next byte, j int) bool {
	switch {
	case word[0] >= 'a' && word[0] <= 'z' && isLetter(prev):
		return true // inside a word: omitempty
	case word == "items" && prev == '"' && (next == '"' || next == '.'):
		return true // the list's JSON key, or a path into it
	case word == "Items" && !isIdent(prev) && !isIdent(next):
		// The list's field, where it's named as one: x.Items, Items: ...,
		// Items []T.
		rest := strings.TrimLeft(s[j:], " \t")
		return prev == '.' || strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "[")
	}
	return false
}

// article makes the "a" or "an" b ends with, if any, the one word takes.
func article(b *strings.Builder, word string) {
	s := b.String()
	an := strings.ContainsRune("aeiouAEIOU", rune(word[0]))
	for _, a := range []string{"an ", "a ", "An ", "A "} {
		if !strings.HasSuffix(s, a) || (len(s) > len(a) && isLetter(s[len(s)-len(a)-1])) {
			continue
		}
		want := a[:1]
		if an {
			want += "n"
		}
		s = s[:len(s)-len(a)] + want + " "
		b.Reset()
		b.WriteString(s)
		return
	}
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIdent(c byte) bool  { return isLetter(c) || c >= '0' && c <= '9' || c == '_' }

// edits add the new resource to each list that names every resource.
var edits = []struct {
	file string
	fn   func(s string, r renamer) (string, error)
}{
	{"cmd/api/resources.go", func(s string, r renamer) (string, error) {
		return appendToList(s, "var resources = []resource{\n", "\t"+r.toPlural+"Resource,\n")
	}},
	{"cmd/api/resources_test.go", func(s string, r renamer) (string, error) {
		return appendToList(s, "var testResources = []testResource{\n", "\t"+r.toPlural+"Test,\n")
	}},
	{"docs/openapi.json", func(s string, r renamer) (string, error) {
		return copyMembers(s, r, []string{`"/items": {`, `"/items/{id}": {`}, []string{`"ItemID": {`},
			[]string{`"Item": {`, `"ItemInput": {`, `"ItemList": {`})
	}},
	{"flagd/flags.json", func(s string, r renamer) (string, error) {
		return copyMembers(s, r, []string{`"items-list-total": {`})
	}},
	{"integration/flagd/flags.json", func(s string, r renamer) (string, error) {
		return copyMembers(s, r, []string{`"items-list-total": {`})
	}},
	{"docs/feature-flags.md", func(s string, r renamer) (string, error) {
		return copyLines(s, r, regexp.MustCompile("^\\| `items-list-total` \\|"))
	}},
	{"perf/k6.js", func(s string, r renamer) (string, error) {
		return copyLines(s, r, regexp.MustCompile(`^  \{ weight: .*route: '[A-Z]+ /items[/'{]`))
	}},
	{"docs/architecture.md", func(s string, r renamer) (string, error) {
		return copyLines(s, r, rowRE)
	}},
	{"docs/curl.md", func(s string, r renamer) (string, error) {
		const before = "\n## Plain curl\n"
		i := strings.Index(s, before)
		if i < 0 {
			return "", errors.New("no \"## Plain curl\" section to put the new resource's examples before")
		}
		return s[:i] + r.text(curlSection) + s[i:], nil
	}},
}

// rowRE matches the package table's rows for the example's packages.
var rowRE = regexp.MustCompile("^\\| `internal/item(/[a-z]+)?` \\|")

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
func copyLines(s string, r renamer, re *regexp.Regexp) (string, error) {
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
				b.WriteString(r.text(c))
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
func copyMembers(s string, r renamer, groups ...[]string) (string, error) {
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
		s = s[:end+1] + ",\n" + indent + r.text(s[start:end+1]) + s[end+1:]
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
