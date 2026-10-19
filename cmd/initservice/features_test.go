package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripMarkers(t *testing.T) {
	in := "a\n// feature:x\nx1\n# feature:y\nxy\n# end feature:y\n// end feature:x\n<!-- feature:y -->\ny1\n<!-- end feature:y -->\nb\n"
	for _, tt := range []struct {
		out  map[string]bool
		want string
	}{
		{nil, "a\nx1\nxy\ny1\nb\n"},
		{map[string]bool{"x": true}, "a\ny1\nb\n"},
		{map[string]bool{"y": true}, "a\nx1\nb\n"},
		{map[string]bool{"x": true, "y": true}, "a\nb\n"},
	} {
		got, err := stripMarkers(in, tt.out)
		if err != nil || got != tt.want {
			t.Errorf("without %v: %q, %v; want %q", tt.out, got, err, tt.want)
		}
	}
	// feature:!x is what's there instead without x.
	inverse := "a\n# feature:x\nwith\n# end feature:x\n# feature:!x\nwithout\n# end feature:!x\n"
	for out, want := range map[bool]string{false: "a\nwith\n", true: "a\nwithout\n"} {
		if got, err := stripMarkers(inverse, map[string]bool{"x": out}); err != nil || got != want {
			t.Errorf("x left out %v: %q, %v; want %q", out, got, err, want)
		}
	}
	for _, bad := range []string{"// feature:x\n", "// end feature:x\n", "// feature:x\n// end feature:y\n"} {
		if _, err := stripMarkers(bad, nil); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	if got, err := stripMarkers("a\n// feature:x\nb\n// end feature:x\nc", nil); err != nil || got != "a\nb\nc" {
		t.Errorf("no final newline: %q, %v", got, err)
	}
}

func TestRemoveMember(t *testing.T) {
	in := "{\n  \"flags\": {\n    \"a\": {\"x\": \"}\"},\n    \"b\": {},\n    \"c\": {}\n  }\n}\n"
	for open, want := range map[string]string{
		`"a": {`: "{\n  \"flags\": {\n    \"b\": {},\n    \"c\": {}\n  }\n}\n",
		`"b": {`: "{\n  \"flags\": {\n    \"a\": {\"x\": \"}\"},\n    \"c\": {}\n  }\n}\n",
		`"c": {`: "{\n  \"flags\": {\n    \"a\": {\"x\": \"}\"},\n    \"b\": {}\n  }\n}\n",
	} {
		got, err := removeMember(in, open)
		if err != nil || got != want {
			t.Errorf("without %s:\n%s%v\nwant\n%s", open, got, err, want)
		}
	}
	if _, err := removeMember(in, `"nope": {`); err == nil {
		t.Error("a member that isn't there: no error")
	}
}

func TestDropLines(t *testing.T) {
	in := "| a |\n| b |\n- item one\n  continued\n- item two\nend\n"
	got, err := dropLines(in, []string{"| b |", "item one"})
	if want := "| a |\n- item two\nend\n"; err != nil || got != want {
		t.Errorf("%q, %v; want %q", got, err, want)
	}
	if _, err := dropLines(in, []string{"nowhere"}); err == nil {
		t.Error("a part matching no line: no error")
	}
}

func TestParseWithout(t *testing.T) {
	got, err := parseWithout(" tracing, tracing ,")
	if err != nil || len(got) != 1 || got[0].Name != "tracing" {
		t.Errorf("%v, %v", got, err)
	}
	if all, err := parseWithout("all"); err != nil || len(all) != len(features) {
		t.Errorf("all: %d, %v; want %d", len(all), err, len(features))
	}
	if _, err := parseWithout("tracing,nope"); err == nil || !strings.Contains(err.Error(), "tracing") {
		t.Errorf("an unknown feature: %v, want an error listing them", err)
	}
	if got, _ := parseWithout(""); len(got) != 0 {
		t.Errorf("nothing: %v", got)
	}
}

func TestListFeatures(t *testing.T) {
	var b strings.Builder
	if err := listFeatures(&b); err != nil {
		t.Fatal(err)
	}
	var list []struct{ Name, Summary string }
	if err := json.Unmarshal([]byte(b.String()), &list); err != nil || len(list) != len(features) {
		t.Fatalf("%s: %v", b.String(), err)
	}
	for _, f := range list {
		if f.Name == "" || f.Summary == "" {
			t.Errorf("%+v: want a name and a summary", f)
		}
	}
}

// TestLeaveOut applies a feature's every kind of rule to a small repository,
// and checks a rule that finds nothing is an error.
func TestLeaveOut(t *testing.T) {
	files := map[string]string{
		"gone/a.go":          "package gone\n",
		"gone.md":            "x\n",
		"table.md":           "| keep |\n| drop |\n",
		"text.md":            "with the feature\n",
		"docs/adr/0007-x.md": "# ADR-0007: X\n\n- **Status:** Accepted\n",
		"code.go":            "package main\n\nvar (\n\tshort = 1\n\t// feature:f\n\tmuchLongerName = 2\n\t// end feature:f\n)\n",
	}
	dir := gitRepo(t, files)
	f := feature{
		Name:      "f",
		remove:    []string{"gone", "gone.md"},
		lines:     map[string][]string{"table.md": {"| drop |"}},
		replace:   map[string][][2]string{"text.md": {{"with the feature", "without it"}}},
		deprecate: []string{"0007"},
	}
	if err := leaveOut(dir, f, nil); err != nil {
		t.Fatal(err)
	}
	if err := applyMarkers(dir, []string{"code.go", "table.md", "gone.md"}, []feature{f}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"table.md":           "| keep |\n",
		"text.md":            "without it\n",
		"docs/adr/0007-x.md": "- **Status:** Deprecated: f was left out of this service (make init)\n",
		"code.go":            "var (\n\tshort = 1\n)\n", // gofmt'd: aligned without the field
	} {
		if got := read(t, filepath.Join(dir, path)); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", path, got, want)
		}
	}
	for _, gone := range []string{"gone", "gone.md"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", gone)
		}
	}

	// A rule for a file another feature removes is skipped.
	if err := leaveOut(dir, feature{Name: "s", lines: map[string][]string{"elsewhere/x.md": {"nope"}}}, map[string]bool{"elsewhere": true}); err != nil {
		t.Errorf("a rule for a file another feature removes: %v", err)
	}

	for name, stale := range map[string]feature{
		"a file that isn't there": {Name: "s", remove: []string{"nope"}},
		"a line that isn't there": {Name: "s", lines: map[string][]string{"table.md": {"nope"}}},
		"text that isn't there":   {Name: "s", replace: map[string][][2]string{"text.md": {{"nope", ""}}}},
		"a record that isn't":     {Name: "s", deprecate: []string{"9999"}},
	} {
		if err := leaveOut(dir, stale, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestInitWithout checks -without on run: an unknown feature is refused
// before anything changes, and -list-features needs no name.
func TestInitWithout(t *testing.T) {
	dir := gitRepo(t, map[string]string{"CHANGELOG.md": "## Unreleased\n\n## [0.6.1] - 2026-10-14\n", "README.md": "x\n"})
	if err := run([]string{"-name", "orders", "-module", "github.com/acme/orders", "-without", "nope"}, dir, &strings.Builder{}); err == nil {
		t.Error("an unknown feature: no error")
	}
	if got := read(t, filepath.Join(dir, "README.md")); got != "x\n" {
		t.Errorf("README changed: %q", got)
	}
	var b strings.Builder
	if err := run([]string{"-list-features"}, dir, &b); err != nil || !strings.Contains(b.String(), `"tracing"`) {
		t.Errorf("-list-features: %v, %s", err, b.String())
	}
}
