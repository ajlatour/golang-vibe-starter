package docs

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// specVersion returns the info.version of the OpenAPI spec in content.
func specVersion(t *testing.T, content string) string {
	t.Helper()
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	return doc.Info.Version
}

// TestSpecVersionIsTheLatestRelease holds docs/openapi.json's info.version to
// the newest release in CHANGELOG.md. make release-prepare sets both in the
// release commit (scripts/spec-version.sh), so this fails only if one is
// changed by hand without the other.
func TestSpecVersionIsTheLatestRelease(t *testing.T) {
	latest := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - `).FindStringSubmatch(read(t, "../CHANGELOG.md"))
	if latest == nil {
		t.Fatal("CHANGELOG.md has no release")
	}
	if got := specVersion(t, read(t, "openapi.json")); got != latest[1] {
		t.Errorf("docs/openapi.json is version %q, but the latest release is %s: make release-prepare sets both", got, latest[1])
	}
}

// specSample is the start of a spec: an info object, and a "version" further
// in that must be left alone.
const specSample = `{
  "openapi": "3.0.3",
  "info": {
    "title": "t",
    "version": "0.0.1",
    "description": "d"
  },
  "components": {
    "schemas": {
      "Build": {
        "properties": {
    "version": "not-me",
          "x": {}
        }
      }
    }
  }
}
`

// specVersionScript runs scripts/spec-version.sh on a file holding content
// and returns its output, exit code, and the file afterwards.
func specVersionScript(t *testing.T, content string, args ...string) (string, int, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{"../scripts/spec-version.sh"}, args...)...)
	cmd.Env = append(os.Environ(), "SPEC="+file)
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	return string(out), cmd.ProcessState.ExitCode(), read(t, file)
}

func TestSpecVersionScript(t *testing.T) {
	out, code, after := specVersionScript(t, specSample, "v1.2.3")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := specVersion(t, after); got != "1.2.3" {
		t.Errorf("info.version = %q, want 1.2.3", got)
	}
	if want := strings.Replace(specSample, `"version": "0.0.1"`, `"version": "1.2.3"`, 1); after != want {
		t.Errorf("changed more than info.version:\n%s", after)
	}

	for name, tt := range map[string]struct{ content, version string }{
		"a malformed version":  {specSample, "1.2"},
		"no info.version line": {strings.Replace(specSample, `    "version": "0.0.1",`+"\n", "", 1), "v1.2.3"},
	} {
		t.Run(name, func(t *testing.T) {
			out, code, unchanged := specVersionScript(t, tt.content, tt.version)
			if code == 0 {
				t.Errorf("exit 0, want a refusal: %s", out)
			}
			if unchanged != tt.content {
				t.Error("changed the file despite refusing")
			}
		})
	}
}
