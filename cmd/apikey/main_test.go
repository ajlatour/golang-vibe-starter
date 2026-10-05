package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
)

func TestRun(t *testing.T) {
	var out strings.Builder
	if err := run([]string{"ci", "write"}, &out); err != nil {
		t.Fatal(err)
	}
	key := regexp.MustCompile(`gvs_\S+`).FindString(out.String())
	entry := regexp.MustCompile(`ci:write:[0-9a-f]{64}`).FindString(out.String())
	if key == "" || entry == "" {
		t.Fatalf("no key or entry in:\n%s", out.String())
	}
	if entry != "ci:write:"+apikey.HashHex(key) {
		t.Errorf("the entry %q isn't the printed key's hash", entry)
	}
	for _, bad := range [][]string{nil, {"ci"}, {"ci", "admin"}, {"CI", "read"}} {
		if err := run(bad, &strings.Builder{}); err == nil {
			t.Errorf("run(%q) = nil error, want one", bad)
		}
	}
}
