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

// changelog runs scripts/changelog.sh on a copy of CHANGELOG.md (or content,
// if given) and returns its output, exit code, and the file afterwards.
func changelog(t *testing.T, content string, args ...string) (string, int, string) {
	t.Helper()
	if content == "" {
		content = read(t, "../CHANGELOG.md")
	}
	file := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{"../scripts/changelog.sh"}, args...)...)
	cmd.Env = append(os.Environ(), "CHANGELOG="+file)
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	return string(out), cmd.ProcessState.ExitCode(), read(t, file)
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

func TestChangelogCutAndNotes(t *testing.T) {
	out, code, after := changelog(t, "", "cut", "v0.1.0")
	if code != 0 {
		t.Fatalf("cut: exit %d: %s", code, out)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if !strings.Contains(after, "\n## Unreleased\n\n## [0.1.0] - "+today+"\n") {
		t.Errorf("after cut, want an empty Unreleased over the release:\n%.400s", after)
	}

	notes, code, _ := changelog(t, after, "notes", "v0.1.0")
	if code != 0 || !strings.HasPrefix(notes, "### ") || strings.Contains(notes, "## [0.1.0]") {
		t.Errorf("notes: exit %d, want the section's body, starting at its first subsection:\n%.200s", code, notes)
	}
	unreleased, _, _ := changelog(t, "", "notes")
	if unreleased != notes {
		t.Error("the release's notes differ from what Unreleased held before the cut")
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
