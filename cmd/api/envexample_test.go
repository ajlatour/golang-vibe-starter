package main

import (
	"bufio"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The API's settings are described in three places: the code that reads them
// (loadConfig), .env.example, and the README's Configuration table. This test
// holds them to each other, so adding, removing or changing the default of a
// setting means updating all three.

// codeVariables returns the environment variables loadConfig reads, recorded
// by giving it a getenv that notes every name it asks for. Only UPSTREAM_URL,
// the one required setting, has a value, so every optional setting is asked
// for (PORT, for one, is read only when ADDR is unset).
func codeVariables(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	_, err := loadConfig(func(name string) string {
		seen[name] = true
		if name == "UPSTREAM_URL" {
			return "http://mock"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("loadConfig with only UPSTREAM_URL set: %v", err)
	}
	var names []string
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// exampleDefaults returns each variable in .env.example and the default it
// shows ("" for none). Every assignment must be commented out, so that copying
// the file to .env changes nothing until a line is uncommented.
func exampleDefaults(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	commented := regexp.MustCompile(`^#\s*([A-Z][A-Z0-9_]*)=(.*)$`)
	active := regexp.MustCompile(`^\s*[A-Z][A-Z0-9_]*=`)
	defaults := map[string]string{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if active.MatchString(text) {
			t.Errorf(".env.example:%d sets %q; every setting must be commented out, showing its default", line, text)
		}
		if m := commented.FindStringSubmatch(text); m != nil {
			if _, dup := defaults[m[1]]; dup {
				t.Errorf(".env.example:%d lists %s twice", line, m[1])
			}
			defaults[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return defaults
}

// readmeDefaults returns each variable in the README's Configuration table and
// its Default column, with backticks removed and "(required)" or blank as "".
func readmeDefaults(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	start := strings.Index(s, "\n## Configuration")
	if start < 0 {
		t.Fatal("README has no Configuration section")
	}
	s = s[start+1:]
	if end := strings.Index(s[3:], "\n## "); end >= 0 {
		s = s[:end+3]
	}
	row := regexp.MustCompile("^\\|\\s*`([A-Z][A-Z0-9_]*)`\\s*\\|([^|]*)\\|")
	defaults := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if m := row.FindStringSubmatch(line); m != nil {
			d := strings.Trim(strings.TrimSpace(m[2]), "`")
			if d == "(required)" {
				d = ""
			}
			defaults[m[1]] = d
		}
	}
	if len(defaults) == 0 {
		t.Fatal("found no variables in the README's Configuration table")
	}
	return defaults
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestSettingsAreDocumented fails if the code, .env.example and the README
// disagree about which settings exist.
func TestSettingsAreDocumented(t *testing.T) {
	code := codeVariables(t)
	example := keys(exampleDefaults(t))
	readme := keys(readmeDefaults(t))
	for _, name := range code {
		if !slices.Contains(example, name) {
			t.Errorf("the code reads %s, but .env.example doesn't list it", name)
		}
		if !slices.Contains(readme, name) {
			t.Errorf("the code reads %s, but the README's Configuration table doesn't list it", name)
		}
	}
	for _, name := range example {
		if !slices.Contains(code, name) {
			t.Errorf(".env.example lists %s, which the code doesn't read", name)
		}
	}
	for _, name := range readme {
		if !slices.Contains(code, name) {
			t.Errorf("the README lists %s, which the code doesn't read", name)
		}
	}
}

// TestDocumentedDefaultsAreTrue fails if a default shown in .env.example
// differs from the README's, or isn't what the code really uses: setting every
// documented default explicitly must give exactly the configuration the code
// produces with nothing set.
func TestDocumentedDefaultsAreTrue(t *testing.T) {
	example, readme := exampleDefaults(t), readmeDefaults(t)
	for name, d := range example {
		if r, ok := readme[name]; ok && r != d {
			t.Errorf("%s: .env.example shows default %q, the README %q", name, d, r)
		}
	}

	withDefaults := map[string]string{"UPSTREAM_URL": "http://mock"}
	for name, d := range example {
		if d != "" {
			withDefaults[name] = d
		}
	}
	explicit, err := loadConfig(func(k string) string { return withDefaults[k] })
	if err != nil {
		t.Fatalf("loading the documented defaults: %v", err)
	}
	implicit, err := loadConfig(func(k string) string {
		if k == "UPSTREAM_URL" {
			return "http://mock"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(explicit, implicit) {
		t.Errorf("the documented defaults don't match the code's:\n documented: %+v\n code:       %+v", explicit, implicit)
	}
}
