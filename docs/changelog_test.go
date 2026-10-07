package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// changelog runs scripts/changelog.sh on a file holding content and returns
// its output, exit code, and the file afterwards.
func changelog(t *testing.T, content string, args ...string) (string, int, string) {
	t.Helper()
	out, code, after, _ := changelogWith(t, content, nil, args...)
	return out, code, after
}

// changelogWith is changelog with a changes directory holding the files in
// changes (name to content). It also returns the names left in it after.
func changelogWith(t *testing.T, content string, changes map[string]string, args ...string) (string, int, string, []string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	changesDir := filepath.Join(dir, "changes")
	if err := os.Mkdir(changesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, c := range changes {
		if err := os.WriteFile(filepath.Join(changesDir, name), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", append([]string{"../scripts/changelog.sh"}, args...)...)
	cmd.Env = append(os.Environ(), "CHANGELOG="+file, "CHANGES="+changesDir)
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(changesDir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	return string(out), cmd.ProcessState.ExitCode(), read(t, file), left
}

// TestChangelogIsReleasable checks CHANGELOG.md has the shape the release
// tooling relies on: an Unreleased section, and releases headed
// "## [X.Y.Z] - YYYY-MM-DD", newest first.
func TestChangelogIsReleasable(t *testing.T) {
	doc := read(t, "../CHANGELOG.md")
	if !strings.Contains(doc, "\n## Unreleased\n") {
		t.Fatal("CHANGELOG.md has no '## Unreleased' section")
	}
	var last string
	for _, h := range regexp.MustCompile(`(?m)^## (.+)$`).FindAllStringSubmatch(doc, -1) {
		if h[1] == "Unreleased" {
			if last != "" {
				t.Error("Unreleased must come before every release")
			}
			continue
		}
		m := regexp.MustCompile(`^\[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})$`).FindStringSubmatch(h[1])
		if m == nil {
			t.Errorf("heading %q: want [X.Y.Z] - YYYY-MM-DD", h[1])
			continue
		}
		if last != "" && m[2] > last {
			t.Errorf("%s is dated after the release above it: newest first", h[1])
		}
		last = m[2]
	}
}

// sample is a changelog with something unreleased and one release before,
// so the tests don't depend on the state of the real one, which every
// release changes.
const sample = `# Changelog

## Unreleased

### Added

- Something new.

### Fixed

- Something broken.

## [0.0.1] - 2026-01-01

### Added

- The start.
`

func TestChangelogCutAndNotes(t *testing.T) {
	out, code, after := changelog(t, sample, "cut", "v0.1.0")
	if code != 0 {
		t.Fatalf("cut: exit %d: %s", code, out)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if !strings.Contains(after, "\n## Unreleased\n\n## [0.1.0] - "+today+"\n\n### Added\n\n- Something new.\n") {
		t.Errorf("after cut, want an empty Unreleased over the release:\n%s", after)
	}
	if !strings.Contains(after, "\n## [0.0.1] - 2026-01-01\n") {
		t.Error("cut lost the earlier release")
	}

	notes, code, _ := changelog(t, after, "notes", "v0.1.0")
	if code != 0 || notes != "### Added\n\n- Something new.\n\n### Fixed\n\n- Something broken.\n\n" {
		t.Errorf("notes: exit %d, got\n%q\nwant the release's body", code, notes)
	}
	unreleased, _, _ := changelog(t, sample, "notes")
	if unreleased != notes {
		t.Error("the release's notes differ from what Unreleased held before the cut")
	}
	older, _, _ := changelog(t, after, "notes", "v0.0.1")
	if older != "### Added\n\n- The start.\n" {
		t.Errorf("notes of the earlier release = %q", older)
	}

	for name, args := range map[string][]string{
		"a version already released":      {"cut", "v0.1.0"},
		"an empty Unreleased":             {"cut", "v0.2.0"},
		"notes of a version not released": {"notes", "v9.9.9"},
		"a malformed version":             {"cut", "0.1"},
	} {
		t.Run(name, func(t *testing.T) {
			out, code, unchanged := changelog(t, after, args...)
			if code == 0 {
				t.Errorf("%v: exit 0, want a refusal", args)
			}
			if unchanged != after {
				t.Errorf("%v changed the file despite refusing: %s", args, out)
			}
		})
	}
}

// TestChangelogGathersChanges checks a release gathers the change files, and
// any entries left under Unreleased, each heading once, in Keep a
// Changelog's order, the files in name order; and removes the files, but
// not the directory's README.
func TestChangelogGathersChanges(t *testing.T) {
	base := "# Changelog\n\n## Unreleased\n\n### Fixed\n\n- Fixed by hand.\n\n## [0.0.1] - 2026-01-01\n\n### Added\n\n- The start.\n"
	changes := map[string]string{
		"README.md":      "# Changes\n\nNot a change: how to write one.\n",
		"b-deadline.md":  "### Fixed\n\n- A deadline.\n\n### Added\n\n- `REQUEST_TIMEOUT`, with a long entry\n  that wraps.\n",
		"a-json-404s.md": "### Fixed\n\n- JSON 404s ([ADR-0042](../docs/adr/0042-x.md)).\n",
		"c-security.md":  "### Security\n\n- Bounded memory.\n\n### Changed\n\n- **Breaking**: something.\n",
	}
	want := "### Added\n\n- `REQUEST_TIMEOUT`, with a long entry\n  that wraps.\n\n" +
		"### Changed\n\n- **Breaking**: something.\n\n" +
		"### Fixed\n\n- Fixed by hand.\n- JSON 404s ([ADR-0042](docs/adr/0042-x.md)).\n- A deadline.\n\n" +
		"### Security\n\n- Bounded memory.\n\n"

	notes, code, _, _ := changelogWith(t, base, changes, "notes")
	if code != 0 || notes != want {
		t.Fatalf("notes: exit %d, got\n%s\nwant\n%s", code, notes, want)
	}
	out, code, after, left := changelogWith(t, base, changes, "cut", "v0.1.0")
	if code != 0 {
		t.Fatalf("cut: exit %d: %s", code, out)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if !strings.Contains(after, "\n## Unreleased\n\n## [0.1.0] - "+today+"\n\n"+want+"## [0.0.1] - 2026-01-01\n") {
		t.Errorf("after cut, want an empty Unreleased over the gathered release:\n%s", after)
	}
	if strings.Join(left, ",") != "README.md" {
		t.Errorf("after cut, changes/ holds %v; want only its README", left)
	}

	for name, bad := range map[string]string{
		"an unknown heading":        "### Improved\n\n- Something.\n",
		"an entry under no heading": "- Something.\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, code, unchanged, left := changelogWith(t, base, map[string]string{"bad.md": bad}, "cut", "v0.1.0")
			if code == 0 || unchanged != base || len(left) != 1 {
				t.Errorf("cut with %s: exit %d; want a refusal, changing nothing", name, code)
			}
		})
	}
	if _, code, _, _ := changelogWith(t, "# Changelog\n\n## Unreleased\n\n## [0.0.1] - 2026-01-01\n", nil, "cut", "v0.1.0"); code == 0 {
		t.Error("cut with nothing unreleased: exit 0, want a refusal")
	}
}

// TestUnreleasedIsInChanges checks the real changelog's Unreleased section
// is empty, its entries being in changes/, and that what's there gathers:
// every file there is a release's entries.
func TestUnreleasedIsInChanges(t *testing.T) {
	doc := read(t, "../CHANGELOG.md")
	if body := section(t, doc, "## Unreleased"); strings.TrimSpace(body) != "" {
		t.Errorf("CHANGELOG.md's Unreleased section has entries; put them in a file in changes/ instead (see changes/README.md):\n%s", body)
	}
	cmd := exec.Command("sh", "scripts/changelog.sh", "notes")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("changes/ doesn't gather: %v\n%s", err, out)
	}
}
