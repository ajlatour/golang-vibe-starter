package docs

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pinnedImageRE matches an image pinned to a digest, as the build pins
// every image it runs: name:tag@sha256:<64 hex>.
var pinnedImageRE = regexp.MustCompile(`[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}`)

// dependabotReads are the files Dependabot keeps image pins current in
// (.github/dependabot.yml): the API's Dockerfile, the compose files, and
// the list of the build's other images, tools/images/Dockerfile.
func dependabotReads(path string) bool {
	switch {
	case path == "cmd/api/Dockerfile", path == "tools/images/Dockerfile":
		return true
	case !strings.Contains(path, "/") && strings.HasPrefix(path, "compose") && strings.HasSuffix(path, ".yaml"):
		return true
	}
	return false
}

// TestImagePinsAreKeptCurrent fails on an image pinned anywhere Dependabot
// doesn't look, where its digest would silently go stale: such an image
// belongs in tools/images/Dockerfile, read with scripts/image.sh. A pin that
// goes stale misses security fixes, and nothing says so.
func TestImagePinsAreKeptCurrent(t *testing.T) {
	checked := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("..", path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || rel == "bin" || strings.HasPrefix(d.Name(), ".") && rel != ".github" && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, "_test.go") {
			return nil // tests name pins to check them
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			pins := pinnedImageRE.FindAllString(line, -1)
			if len(pins) == 0 {
				continue
			}
			checked += len(pins)
			if !dependabotReads(rel) {
				t.Errorf("%s:%d pins %s, which Dependabot won't keep current: list it in tools/images/Dockerfile and read it with scripts/image.sh", rel, i+1, pins[0])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 5 {
		t.Fatalf("found only %d pinned images; the repository or this test have changed shape", checked)
	}
	deps := read(t, "../.github/dependabot.yml")
	for _, dir := range []string{`"/cmd/api"`, `"/tools/images"`} {
		if !strings.Contains(deps, dir) {
			t.Errorf(".github/dependabot.yml doesn't watch %s", dir)
		}
	}
}

// TestToolImagesResolve checks scripts/image.sh finds every image in
// tools/images/Dockerfile, each pinned, and that each is used somewhere.
func TestToolImagesResolve(t *testing.T) {
	list := read(t, "../tools/images/Dockerfile")
	names := regexp.MustCompile(`(?m)^FROM \S+ AS (\S+)$`).FindAllStringSubmatch(list, -1)
	if len(names) == 0 {
		t.Fatal("tools/images/Dockerfile lists no images")
	}
	uses := read(t, "../Makefile")
	// feature:release
	uses += read(t, "../.github/workflows/release.yml")
	// end feature:release
	for _, n := range names {
		out, err := exec.Command("sh", "../scripts/image.sh", n[1]).Output()
		if ref := strings.TrimSpace(string(out)); err != nil || !pinnedImageRE.MatchString(ref) {
			t.Errorf("scripts/image.sh %s = %q, %v; want a pinned image", n[1], ref, err)
		}
		if !strings.Contains(uses, "scripts/image.sh "+n[1]) {
			t.Errorf("tools/images/Dockerfile lists %s, which nothing uses: remove it", n[1])
		}
	}
	if err := exec.Command("sh", "../scripts/image.sh", "no-such-image").Run(); err == nil {
		t.Error("scripts/image.sh of an unknown name succeeded")
	}
}
