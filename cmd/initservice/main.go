// Command initservice turns a copy of this template into a new service of
// its own: `make init NAME=orders MODULE=github.com/acme/orders`. See
// docs/development.md#starting-a-new-service.
//
// It rewrites every file under version control:
//
//   - the module path, github.com/ajlatour/golang-vibe-starter, becomes MODULE,
//     in every go.mod and import;
//   - the GitHub repository, ajlatour/golang-vibe-starter, becomes MODULE's
//     owner and repository when MODULE is on github.com (the workflows check
//     which repository they run in);
//   - the name, golang-vibe-starter, becomes NAME: the compose project, the
//     images, the spec's title and the docs;
//   - the code owners become MODULE's owner, when it's on github.com.
//
// And it starts the service's history: CHANGELOG.md holds one entry, 0.0.0,
// saying which template version it came from; changes/ is emptied but for its
// README; the spec is version 0.0.0; and the template's own audit page goes.
// LICENSE is left as it is: whose copyright, and which license, is for the
// service's owners to decide. Everything else stays, the example /items
// resource and the decision records included.
//
// It runs once, on a fresh copy, and says what's left to do by hand.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/rename"
)

// What the template calls itself.
const (
	templateModule = "github.com/ajlatour/golang-vibe-starter"
	templateRepo   = "ajlatour/golang-vibe-starter"
	templateName   = "golang-vibe-starter"
	templateOwner  = "@ajlatour"
)

// The template's own history, which a new service doesn't inherit.
var templateOnly = []string{"docs/audit-2026-10.md"}

var (
	nameRE   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	moduleRE = regexp.MustCompile(`^[a-z0-9.-]+(/[A-Za-z0-9._~-]+)+$`)
)

func main() {
	if err := run(os.Args[1:], ".", os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		os.Exit(1)
	}
}

func run(args []string, dir string, out io.Writer) error {
	fs := flag.NewFlagSet("initservice", flag.ContinueOnError)
	name := fs.String("name", "", "the service's name: lower-case letters, digits and dashes, e.g. orders")
	module := fs.String("module", "", "its Go module path, e.g. github.com/acme/orders")
	withoutList := fs.String("without", "", "features to leave out, comma-separated, e.g. tracing, or all (-list-features lists them)")
	list := fs.Bool("list-features", false, "list the features -without can leave out, as JSON, and stop")
	resource := fs.String("resource", "", "the example resource's name in this service, singular, e.g. order (default: item, as in the template)")
	resourcePlural := fs.String("resource-plural", "", "its plural, if it isn't the name with an s, e.g. categories")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *list {
		return listFeatures(out)
	}
	without, err := parseWithout(*withoutList)
	if err != nil {
		return err
	}
	resourceRename, err := parseResource(*resource, *resourcePlural)
	if err != nil {
		return err
	}
	if !nameRE.MatchString(*name) {
		return fmt.Errorf("name %q: want lower-case letters, digits and dashes, starting with a letter: it names images and the compose project", *name)
	}
	if !moduleRE.MatchString(*module) || *module == templateModule {
		return fmt.Errorf("module %q: want a module path of your own, such as github.com/acme/orders", *module)
	}
	version, err := templateVersion(dir)
	if err != nil {
		return err
	}

	repo, owner := "", ""
	if rest, ok := strings.CutPrefix(*module, "github.com/"); ok {
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) >= 2 {
			repo, owner = parts[0]+"/"+parts[1], "@"+parts[0]
		}
	}
	replacer := strings.NewReplacer(templateModule, *module, templateRepo, repo, templateName, *name)
	if repo == "" {
		replacer = strings.NewReplacer(templateModule, *module, templateName, *name)
	}

	files, err := tracked(dir)
	if err != nil {
		return err
	}
	// The features first, while the files say what their rules expect; and
	// this tool, unless it's a copy of the template without it (a test's).
	leave := without
	if _, err := os.Stat(filepath.Join(dir, "cmd/initservice")); err == nil {
		leave = append(slices.Clip(without), initFeature)
	}
	if err := leaveAll(dir, leave); err != nil {
		return err
	}
	if err := applyMarkers(dir, files, leave); err != nil {
		return err
	}
	if resourceRename != nil {
		if files, err = renameResource(dir, files, *resourceRename); err != nil {
			return err
		}
	}

	changed := 0
	for _, f := range files {
		// This tool names the template on purpose, to know what to replace.
		if f == "LICENSE" || f == "CHANGELOG.md" || contains(templateOnly, f) || strings.HasPrefix(f, "cmd/initservice/") {
			continue
		}
		path := filepath.Join(dir, f)
		b, err := readFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // deleted in the working tree
			}
			return err
		}
		if bytes.IndexByte(b, 0) >= 0 {
			continue // binary
		}
		s := replacer.Replace(string(b))
		if f == ".github/CODEOWNERS" {
			s = codeOwners(s, owner)
		}
		if s != string(b) {
			if err := writeFile(path, []byte(s)); err != nil {
				return err
			}
			changed++
		}
	}

	if err := startHistory(dir, *name, version); err != nil {
		return err
	}
	for _, f := range templateOnly {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := dropAuditLinks(dir); err != nil {
		return err
	}
	if err := startRoadmap(dir, *name); err != nil {
		return err
	}
	if slices.ContainsFunc(without, func(f feature) bool { return f.tidy }) {
		if err := tidy(dir); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "Initialized %s (%s) from %s %s: %d files rewritten.\n", *name, *module, templateName, version, changed)
	for _, f := range without {
		fmt.Fprintf(out, "Left out: %s.\n", f.Name)
	}
	if resourceRename != nil {
		fmt.Fprintf(out, "The example resource is /%s.\n", resourceRename.ToPlural)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Left to do by hand:")
	fmt.Fprintln(out, "  - LICENSE: set the copyright holder, or choose another license.")
	if owner == "" {
		fmt.Fprintln(out, "  - .github/CODEOWNERS: name who reviews what.")
	}
	fmt.Fprintln(out, "  - Create the repository, push, and in its settings turn on private vulnerability reporting (SECURITY.md).")
	fmt.Fprintln(out, "  - README.md: say what this service is for.")
	example := "/items"
	if resourceRename != nil {
		example = "/" + resourceRename.ToPlural
		fmt.Fprintf(out, "  - The resource, %s: give it the fields it's meant to have (docs/development.md#adding-a-resource lists where).\n", example)
	} else {
		fmt.Fprintf(out, "  - The example resource, %s: keep it as a guide, or remove it once the service has its own.\n", example)
	}
	fmt.Fprintln(out, "  - Run make check, make lint and make docs-check, and commit.")
	return nil
}

// resourceNameRE is what a resource's name may be: a Go package's name, and
// its path.
var resourceNameRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,30}$`)

// parseResource reads -resource and -resource-plural: the example
// resource's new name, or nil to keep it as item.
func parseResource(name, plural string) (*rename.Renamer, error) {
	if name == "" {
		if plural != "" {
			return nil, errors.New("-resource-plural without -resource")
		}
		return nil, nil
	}
	if plural == "" {
		plural = name + "s"
	}
	for _, n := range []string{name, plural} {
		if !resourceNameRE.MatchString(n) {
			return nil, fmt.Errorf("-resource %q: want lower-case letters and digits, starting with a letter: it names a Go package and the resource's path", n)
		}
	}
	if name == plural {
		return nil, fmt.Errorf("-resource %q: its plural is the same; give one with -resource-plural", name)
	}
	if name == exampleResource || plural == rename.ListKey {
		return nil, nil // the template's own name: nothing to do
	}
	return &rename.Renamer{From: exampleResource, FromPlural: rename.ListKey, To: name, ToPlural: plural}, nil
}

// exampleResource is the template's example resource, /items.
const exampleResource = "item"

// renameResource renames the example resource across files, paths and
// contents, but in the license, the changelog and the renaming tools
// themselves, and returns the files' new names.
func renameResource(dir string, files []string, r rename.Renamer) ([]string, error) {
	var out []string
	for _, f := range files {
		path := filepath.Join(dir, f)
		b, err := readFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // a feature's file, gone
		}
		if err != nil {
			return nil, err
		}
		to := f
		if !(f == "LICENSE" || f == "CHANGELOG.md" || strings.HasPrefix(f, "cmd/initservice/") || strings.HasPrefix(f, "internal/rename/")) {
			to = r.Text(f)
			if bytes.IndexByte(b, 0) < 0 {
				s := r.Text(string(b))
				if strings.HasSuffix(f, ".go") && s != string(b) {
					// Renamed, names change length, and so does alignment.
					var err error
					if s, err = tidyGo(s); err != nil {
						return nil, fmt.Errorf("%s: %w", f, err)
					}
				}
				b = []byte(s)
			}
		}
		out = append(out, to)
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		dest := filepath.Join(dir, to)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil { // #nosec G301 -- a source directory, as checked in
			return nil, err
		}
		if err := os.WriteFile(dest, b, info.Mode().Perm()); err != nil { // #nosec G306 G703 -- a file in the repository being initialized, as checked in
			return nil, err
		}
		// The old name goes once the new one's written, and its directory
		// with it if that leaves it empty.
		if to != f {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			removeEmptyDirs(dir, filepath.Dir(f))
		}
	}
	return out, nil
}

// removeEmptyDirs removes rel, under dir, and each of its parents, while
// they're empty.
func removeEmptyDirs(dir, rel string) {
	for rel != "." && rel != "" {
		if os.Remove(filepath.Join(dir, rel)) != nil {
			return
		}
		rel = filepath.Dir(rel)
	}
}

// startRoadmap replaces the template's roadmap, which is its own, with a
// page for the service's.
func startRoadmap(dir, name string) error {
	path := filepath.Join(dir, "ROADMAP.md")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return writeFile(path, []byte(fmt.Sprintf(`# Roadmap

What's planned for %s: what's done, queued and proposed. Started from the
[%s](https://github.com/%s) template, whose own roadmap is in its repository.

**Status:** ✅ done · 🔄 in progress · ⏭️ queued (decided, not yet built) · 💡 proposed.

Nothing yet.
`, name, templateName, templateRepo)))
}

// tidy runs go mod tidy in dir, after a feature that was a
// dependency is left out, and keeps a go.sum, if empty: CI keys its caches
// on it.
func tidy(dir string) error {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy: %w\n%s", err, out)
	}
	sum := filepath.Join(dir, "go.sum")
	if _, err := os.Stat(sum); errors.Is(err, os.ErrNotExist) {
		return writeFile(sum, nil)
	}
	return nil
}

// tracked lists the files under version control in dir.
func tracked(dir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
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
	return files, nil
}

// templateVersion is the template's latest release, from its changelog.
func templateVersion(dir string) (string, error) {
	b, err := readFile(filepath.Join(dir, "CHANGELOG.md"))
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - `).FindSubmatch(b)
	if m == nil {
		return "", errors.New("CHANGELOG.md has no release: is this the template, not yet initialized?")
	}
	return "v" + string(m[1]), nil
}

// codeOwners makes the template's owner the new one, or, with none to name,
// leaves the rule commented out for a person to fill in.
func codeOwners(s, owner string) string {
	if owner != "" {
		return strings.ReplaceAll(s, templateOwner, owner)
	}
	return strings.ReplaceAll(s, "* "+templateOwner, "# * @your-team")
}

// startHistory gives the service a changelog of its own: one entry, the
// version it started at, saying where it came from. And the spec says the
// same version, as a release keeps it (make release-prepare).
func startHistory(dir, name, version string) error {
	path := filepath.Join(dir, "CHANGELOG.md")
	b, err := readFile(path)
	if err != nil {
		return err
	}
	s := string(b)
	i := strings.Index(s, "\n## Unreleased")
	if i < 0 {
		return errors.New("CHANGELOG.md has no Unreleased section")
	}
	s = strings.ReplaceAll(s[:i], templateName, name) + fmt.Sprintf(`
## Unreleased

## [0.0.0] - %s

### Added

- Started from the [%s](https://github.com/%s) template, %s.
`, time.Now().UTC().Format("2006-01-02"), templateName, templateRepo, version)
	if err := writeFile(path, []byte(s)); err != nil {
		return err
	}

	entries, err := os.ReadDir(filepath.Join(dir, "changes"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if e.Name() != "README.md" && strings.HasSuffix(e.Name(), ".md") {
			if err := os.Remove(filepath.Join(dir, "changes", e.Name())); err != nil {
				return err
			}
		}
	}

	spec := filepath.Join(dir, "docs", "openapi.json")
	b, err = readFile(spec)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`("info"\s*:\s*\{[^}]*?"version"\s*:\s*")[^"]*(")`)
	if !re.Match(b) {
		return errors.New("docs/openapi.json: no info.version to set")
	}
	return writeFile(spec, re.ReplaceAll(b, []byte("${1}0.0.0${2}")))
}

// dropAuditLinks removes the template's audit page from the docs site's
// navigation and the README's list of documents.
func dropAuditLinks(dir string) error {
	// The page's line in the navigation, the newline before it with it, so
	// the list's closing quote stays where it is, wherever the line falls.
	if err := removeLine(filepath.Join(dir, "docs/site/build.sh"), "\nArchitecture|docs/audit-2026-10.md|audit-2026-10.html"); err != nil {
		return err
	}
	readme := filepath.Join(dir, "README.md")
	b, err := readFile(readme)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^- \[Audit, October 2026\]\(docs/audit-2026-10\.md\):[^\n]*\n(  [^\n]*\n)*`)
	return writeFile(readme, re.ReplaceAll(b, nil))
}

func removeLine(path, line string) error {
	b, err := readFile(path)
	if err != nil {
		return err
	}
	return writeFile(path, []byte(strings.Replace(string(b), line, "", 1)))
}

// readFile and writeFile read and write the files of the repository being
// initialized: the one its user named, in which they run it.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 -- a file in the repository being initialized
}

// writeFile keeps a file's mode, so scripts stay executable.
func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o644) // #nosec G306 G703 -- a file in the repository being initialized, a source file as checked in
}

func contains(list []string, s string) bool { return slices.Contains(list, s) }
