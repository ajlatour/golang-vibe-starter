package perf

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// git runs git in dir and returns its output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// clone makes a checkout of remote's main, as a workflow has.
func clone(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "clone", "-q", remote, ".")
	return dir
}

// save runs save-record.sh in checkout, saving content as a record of sha in
// dir, and returns its exit code and output.
func save(t *testing.T, checkout, dir, sha, content string, env ...string) (int, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("save-record.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, dir, file, "Record "+sha[:7])
	cmd.Dir = checkout
	cmd.Env = append(append(os.Environ(), env...), "RECORD_SHA="+sha, "GITHUB_SHA=0000000000",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, _ := cmd.CombinedOutput()
	return cmd.ProcessState.ExitCode(), string(out)
}

// TestSaveRecord runs perf/save-record.sh against a throwaway remote: it
// creates perf-data, adds to it, names each record by its commit (RECORD_SHA,
// not GITHUB_SHA), and when another record lands between its fetch and its
// push, rebases onto it and pushes again.
func TestSaveRecord(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	remote := t.TempDir()
	git(t, remote, "init", "-q", "--bare", "-b", "main")
	seed := t.TempDir()
	git(t, seed, "init", "-q", "-b", "main")
	git(t, seed, "commit", "-q", "--allow-empty", "-m", "start")
	git(t, seed, "push", "-q", remote, "main")

	// Two workflows' checkouts, made before either has saved anything.
	a, b := clone(t, remote), clone(t, remote)
	if code, out := save(t, a, "records", "aaaaaaa111", `{"sha":"aaaaaaa111"}`); code != 0 {
		t.Fatalf("first save, creating perf-data: exit %d\n%s", code, out)
	}
	// b fetches perf-data afresh, so it adds to the branch a created...
	if code, out := save(t, b, "ci", "bbbbbbb222", `{"sha":"bbbbbbb222"}`); code != 0 {
		t.Fatalf("second save: exit %d\n%s", code, out)
	}
	// a saves again, and just before its push, b saves another record: a's
	// push is refused, and it must rebase onto b's and push again. A pre-push
	// hook makes the race happen, once.
	script, _ := filepath.Abs("save-record.sh")
	hook := filepath.Join(a, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte(`#!/bin/sh
[ -e "$MARK" ] && exit 0
touch "$MARK"
echo '{"sha":"ddddddd444"}' > "$MARK.json"
# Git runs hooks pointed at the pushing repository; the other save is in b.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX GIT_COMMON_DIR
cd "$OTHER" && RECORD_SHA=ddddddd444 sh "$SCRIPT" ci "$MARK.json" "Record ddddddd"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	code, out := save(t, a, "ci", "ccccccc333", `{"sha":"ccccccc333"}`,
		"MARK="+filepath.Join(t.TempDir(), "raced"), "OTHER="+b, "SCRIPT="+script)
	if code != 0 {
		t.Fatalf("third save, racing a fourth: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "push 1 failed; rebasing") {
		t.Errorf("the race didn't happen: want the first push refused and retried\n%s", out)
	}

	files := strings.Fields(git(t, remote, "ls-tree", "-r", "--name-only", "perf-data"))
	var records []string
	for _, f := range files {
		if f != "README.md" {
			records = append(records, f)
		}
	}
	if len(records) != 4 || !slices.Contains(files, "README.md") {
		t.Fatalf("perf-data holds %q, want a README and 4 records", files)
	}
	for _, want := range []string{"records/*-aaaaaaa.json", "ci/*-bbbbbbb.json", "ci/*-ccccccc.json", "ci/*-ddddddd.json"} {
		found := false
		for _, f := range records {
			if ok, _ := filepath.Match(want, f); ok {
				found = true
			}
		}
		if !found {
			t.Errorf("no record matching %s in %q", want, records)
		}
	}
	if log := git(t, remote, "log", "--format=%s", "perf-data"); log != "Record ccccccc\nRecord ddddddd\nRecord bbbbbbb\nRecord aaaaaaa\n" {
		t.Errorf("perf-data's history = %q, want the four saves, the rebased one last", log)
	}
}
