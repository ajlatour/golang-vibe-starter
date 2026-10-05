// Package perf holds the k6 performance tests (k6.js, profiles.json) and the
// scripts that run them. Its only Go code is this test, which checks the k6
// files without running k6; CI's load-smoke job and make perf run them.
package perf

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type phase struct {
	Name     string
	Rate     int
	From     *int
	Duration string
}

type profile struct {
	Description   string
	Informational bool
	Phases        []phase
	Thresholds    map[string][]string
}

func loadProfiles(t *testing.T) map[string]profile {
	t.Helper()
	b, err := os.ReadFile("profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var profiles map[string]profile
	if err := dec.Decode(&profiles); err != nil {
		t.Fatalf("profiles.json: %v", err)
	}
	return profiles
}

// seconds parses a duration the way k6.js does: whole s, m or h only.
func seconds(d string) (int, error) {
	m := regexp.MustCompile(`^(\d+)(s|m|h)$`).FindStringSubmatch(d)
	if m == nil {
		return 0, fmt.Errorf("bad duration %q: use a whole number of s, m or h", d)
	}
	n, _ := strconv.Atoi(m[1])
	return n * map[string]int{"s": 1, "m": 60, "h": 3600}[m[2]], nil
}

var (
	// A threshold key: a metric k6.js produces, optionally narrowed to a phase.
	thresholdKeyRE = regexp.MustCompile(`^(http_req_failed|http_req_duration|checks)(?:\{phase:([a-z0-9-]+)\})?$`)
	// A threshold expression: an aggregation, a comparison and a number.
	thresholdRE = regexp.MustCompile(`^(rate|avg|med|min|max|p\(\d+(?:\.\d+)?\)) *(<|<=|>|>=) *\d+(?:\.\d+)?$`)
)

func TestProfiles(t *testing.T) {
	profiles := loadProfiles(t)
	for _, name := range []string{"smoke", "load", "stress", "spike", "soak"} {
		if _, ok := profiles[name]; !ok {
			t.Errorf("profiles.json has no %q profile; the docs and CI rely on it", name)
		}
	}
	for name, p := range profiles {
		t.Run(name, func(t *testing.T) {
			if !regexp.MustCompile(`^[a-z-]+$`).MatchString(name) {
				t.Errorf("name %q: use lower-case letters and dashes (make help lists the names with sed)", name)
			}
			if p.Description == "" {
				t.Error("no description")
			}
			// perf/run.sh finds a profile's "informational" key by matching
			// braces, which a brace in a description would throw off.
			if strings.ContainsAny(p.Description, "{}") {
				t.Error("the description contains a brace, which perf/run.sh can't cope with")
			}
			if len(p.Phases) == 0 {
				t.Fatal("no phases")
			}
			phases := map[string]bool{}
			total := 0
			for _, ph := range p.Phases {
				if ph.Name == "" || phases[ph.Name] {
					t.Errorf("phase %q: every phase needs a unique name", ph.Name)
				}
				phases[ph.Name] = true
				if ph.Rate <= 0 || (ph.From != nil && *ph.From < 0) {
					t.Errorf("phase %q: rate must be positive and from not negative", ph.Name)
				}
				s, err := seconds(ph.Duration)
				if err != nil || s == 0 {
					t.Errorf("phase %q: %v", ph.Name, err)
				}
				total += s
			}
			if name == "smoke" && total > 60 {
				t.Errorf("smoke runs %ds; keep it under a minute, as CI runs it on every pull request", total)
			}

			metrics := map[string]bool{}
			for key, exprs := range p.Thresholds {
				m := thresholdKeyRE.FindStringSubmatch(key)
				if m == nil {
					t.Errorf("threshold %q: want http_req_failed, http_req_duration or checks, optionally with {phase:<name>}", key)
					continue
				}
				if m[2] != "" && !phases[m[2]] {
					t.Errorf("threshold %q names phase %q, which this profile doesn't have", key, m[2])
				}
				metrics[m[1]] = true
				for _, e := range exprs {
					if !thresholdRE.MatchString(e) {
						t.Errorf("threshold %q: can't read %q", key, e)
					}
				}
			}
			if !metrics["http_req_failed"] || !metrics["http_req_duration"] {
				t.Error("every profile needs thresholds on both http_req_failed and http_req_duration")
			}
		})
	}
}

// TestTrafficCoversTheAPI fails if an operation in docs/openapi.json is
// missing from the traffic mix in k6.js, so a new route gets load-tested.
func TestTrafficCoversTheAPI(t *testing.T) {
	b, err := os.ReadFile("../docs/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("k6.js")
	if err != nil {
		t.Fatal(err)
	}
	for path, ops := range spec.Paths {
		for method := range ops {
			switch {
			case !slices.Contains([]string{"get", "put", "post", "delete", "patch", "head", "options"}, method):
				continue // "parameters" and other path-level keys
			case path == "/healthz":
				continue // the liveness probe isn't traffic
			}
			route := strings.ToUpper(method) + " " + path
			if !strings.Contains(string(script), "route: '"+route+"'") {
				t.Errorf("k6.js's traffic mix has no %s; add it to the mix", route)
			}
		}
	}
}
