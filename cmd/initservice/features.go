package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A feature is a part of the template a new service can leave out:
// -without tracing. What it is in the code is marked where it's mixed with
// the rest, and listed here where it's a file of its own or a line in a
// table:
//
//   - Blocks between marker lines, in any text file: a line that is only
//     "// feature:NAME", "# feature:NAME", "<!-- feature:NAME -->" or
//     "/* feature:NAME */", and
//     its end, the same with "end feature:NAME". Left out, the block goes;
//     kept, only the marker lines do, so no service has them. A block marked
//     feature:!NAME is the opposite: what's there instead without NAME.
//   - remove: files and directories that are the feature's alone.
//   - lines: per file, lines that go with it, each found by a part of it: a
//     table row, a list item (its continuation lines too), a menu entry.
//   - replace: per file, text that says something else without it.
//   - deprecate: its decision records, which stay, as history, marked
//     Deprecated so nothing holds the code to them.
//   - gone: words nothing may name once it's left out, which the template
//     check (TestInitOnACopy) looks for, so a mention the list misses is
//     caught.
//
// Every rule must find what it names, so the list can't quietly fall behind
// the template: run on a copy of it, a stale rule is an error.
type feature struct {
	Name, Summary string
	remove        []string
	lines         map[string][]string
	replace       map[string][][2]string
	deprecate     []string
	gone          []string
	// tidy runs go mod tidy after, for a feature that's a dependency.
	tidy bool
}

// initFeature is make init itself, which every service leaves out: it's
// run once, and its rules are the template's.
var initFeature = feature{
	Name:   "init",
	remove: []string{"cmd/initservice"},
	lines: map[string][]string{
		"docs/architecture.md": {"| `cmd/initservice` |"},
		"docs/development.md":  {"- [Starting a new service](#starting-a-new-service)"},
	},
	replace: map[string][][2]string{
		"README.md":                {{"To start a new service from this template, copy it and run `make init NAME=orders\nMODULE=github.com/acme/orders`: [Starting a new service](docs/development.md#starting-a-new-service)\nsays what it changes. `make resource NAME=order` then adds", "`make resource NAME=order` adds"}},
		".github/workflows/ci.yml": {{"  # make init, and make resource, each on a copy of the repository, then the\n  # result vetted and its tests run: so starting a new service from the\n  # template, and adding a resource to it, keep working as the template\n  # changes.", "  # make resource on a copy of the repository, then the result vetted and\n  # its tests run: so adding a resource keeps working as the code changes."}},
		"docs/development.md":      {{"- **template**: `make init-check` and `make resource-check`: [`make init`](#starting-a-new-service),\n  and [`make resource`](#adding-a-resource), each on a copy of the repository, then the result\n  vetted and its tests run, so both keep working as the template changes.", "- **template**: `make resource-check`: [`make resource`](#adding-a-resource) on a copy of the\n  repository, then the result vetted and its tests run, so it keeps working as the code changes."}},
	},
	gone: []string{"make init", "cmd/initservice", "init-check", "#starting-a-new-service"},
}

// features are what -without can leave out.
var features = []feature{
	{
		Name:    "tracing",
		Summary: "OpenTelemetry spans for every request and call, exported over OTLP, and Jaeger in the local stack (make tracing). Off until OTEL_EXPORTER_OTLP_ENDPOINT is set; the service's only third-party dependency.",
		remove:  []string{"internal/telemetry", "cmd/api/telemetry.go", "cmd/api/telemetry_test.go", "docs/tracing.md"},
		lines: map[string][]string{
			"README.md":            {"| `OTEL_EXPORTER_OTLP_", "make tracing ", "- [Tracing](docs/tracing.md)"},
			"docs/logging.md":      {"| `using tracing` |", "| `tracing failed` |"},
			"docs/architecture.md": {"| `internal/telemetry` |"},
			"docs/site/build.sh":   {"|docs/tracing.md|"},
		},
		replace: map[string][][2]string{
			"README.md":            {{"A Go HTTP API built on the standard library, with one optional exception: the OpenTelemetry SDK, for\n[tracing](docs/tracing.md), in a package of its own that's off by default.", "A Go HTTP API built on the standard library only."}},
			"docs/logging.md":      {{", `using tracing` |", " |"}},
			"SECURITY.md":          {{"no third-party dependencies in the service but the optional OpenTelemetry SDK, in one package,", "no third-party dependencies in the service,"}},
			"docs/architecture.md": {{"but for the adapters allowed a third-party module: `internal/telemetry`, the OpenTelemetry SDK. Each is", "but for the adapters allowed a third-party module, which `docs/architecture_test.go` lists: none yet. Each would be"}},
			"docs/development.md":  {{"but for adapters allowed a dependency: so far one,\n  `internal/telemetry`, the OpenTelemetry SDK, optional and removable\n  ([ADR-0046]", "but for adapters allowed a dependency, none yet, each\n  optional and removable\n  ([ADR-0046]"}},
			"docs/standards.md":    {{"but in adapters\n   allowed one, so far the OpenTelemetry SDK\n   ([ADR-0046]", "but in adapters\n   allowed one, none yet\n   ([ADR-0046]"}},
		},
		deprecate: []string{"0046"},
		gone:      []string{"internal/telemetry", "OTEL_EXPORTER", "jaeger", "docs/tracing.md", "make tracing"},
		tidy:      true,
	},
	{
		Name:    "perf",
		Summary: "Performance testing: k6 load profiles and their CI comparison on every pull request (with an allocation gate), main's performance history and a nightly soak, CI-time records, and a Grafana dashboard in the local stack (make monitor).",
		remove: []string{"perf", "monitoring", "docs/performance.md", ".github/workflows/perf.yml",
			".github/workflows/perf-trends.yml", ".github/workflows/soak.yml", ".github/workflows/ci-times.yml"},
		lines: map[string][]string{
			"README.md": {"make perf PROFILE=load", "make perf-compare", "make perf-trends", "make perf-soak",
				"perf/                k6", "monitoring/          Prometheus", "- [Performance](docs/performance.md)"},
			".github/workflows/ci.yml":      {"perf/report/go.sum"},
			".github/workflows/release.yml": {"perf/report/go.sum"},
			"docs/site/build.sh":            {"|docs/performance.md|", "|+history|"},
			"docs/development.md": {"| A benchmarked path, adding allocations on purpose |", "| A route (new or removed) | The traffic mix",
				"| A Go runtime metric's name, or a k6 percentile", "| A k6 profile or threshold |"},
			"docs/releasing.md": {"| The performance records,"},
		},
		replace: map[string][][2]string{
			"docs/architecture.md": {
				{"`127.0.0.1` only. Opt-in profiles add k6 (`perf`) and cAdvisor, Prometheus and Grafana\n(`monitoring`).", "`127.0.0.1` only."},
				{"`.github/dependabot.yml`, review: `.github/workflows/perf.yml` |", "`.github/dependabot.yml` |"},
			},
			"docs/standards.md":             {{"`integration/`, `perf/report/` |", "`integration/` |"}, {"(`integration/`, `perf/report/`).", "(`integration/`)."}},
			"docs/logging.md":               {{"profiles are the next step: [Performance](performance.md#profiling).", "profiles are the next step: pprof, served on `PPROF_ADDR`."}},
			"README.md":                     {{"Keep it private: see [Performance](docs/performance.md#profiling).", "Keep it private."}},
			".github/workflows/release.yml": {{"contents: write # the GitHub release, and reading the perf-data branch", "contents: write # the GitHub release"}},
			"docs/releasing.md":             {{"(open `docs/index.html`), with a Performance history page charting `main`'s performance and CI time up to the release |", "(open `docs/index.html`) |"}},
		},
		deprecate: []string{"0013", "0014", "0024"},
		gone:      []string{"perf/k6", "perf/report", "perf/run", "performance.md", "make perf", "make monitor", "perf-data", "perf-ok", "Grafana", "perf.yml"},
	},
	{
		Name:    "release",
		Summary: "Publishing a release: the Release workflow, which builds the image for amd64 and arm64 with an SBOM and provenance, smoke-tests it and pushes it to GHCR, and the GitHub release with its docs. Versioning stays: the changelog, make release-prepare and release-tag.",
		remove:  []string{".github/workflows/release.yml", "scripts/smoke-image.sh", "compose.smoke.yaml"},
		lines: map[string][]string{
			"docs/architecture.md": {"| DEL-2 |", "- **Images aren't signed:**"},
		},
		replace: map[string][][2]string{
			"docs/architecture.md": {{"`docs/changelog_test.go`, review: `.github/workflows/release.yml` |", "`docs/changelog_test.go` |"}},
			"docs/standards.md":    {{"release-tag`, and its image is smoke-tested before it's published ([Releasing](releasing.md)).", "release-tag` ([Releasing](releasing.md))."}},
			"README.md":            {{"cutting a version, and the image, SBOM and provenance it\n  publishes.", "cutting a version."}},
		},
		deprecate: []string{"0027"},
		gone:      []string{"release.yml", "smoke-image", "make smoke", "SBOM", "docs-release", "compose.smoke"},
	},
}

func featureNamed(name string) (feature, bool) {
	i := slices.IndexFunc(features, func(f feature) bool { return f.Name == name })
	if i < 0 {
		return feature{}, false
	}
	return features[i], true
}

// listFeatures writes the features, as JSON, for a tool to offer them
// (govibe).
func listFeatures(out io.Writer) error {
	type entry struct{ Name, Summary string }
	var list []entry
	for _, f := range features {
		list = append(list, entry{f.Name, f.Summary})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

// parseWithout reads -without's comma-separated feature names, or "all".
func parseWithout(s string) ([]feature, error) {
	var out []feature
	for name := range strings.SplitSeq(s, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if name == "all" {
			return slices.Clone(features), nil
		}
		f, ok := featureNamed(name)
		if !ok {
			var names []string
			for _, f := range features {
				names = append(names, f.Name)
			}
			return nil, fmt.Errorf("-without %s: no such feature; there are %s", name, strings.Join(names, ", "))
		}
		if !slices.ContainsFunc(out, func(o feature) bool { return o.Name == f.Name }) {
			out = append(out, f)
		}
	}
	return out, nil
}

var markerRE = regexp.MustCompile(`^\s*(?://|#|<!--|/\*)\s*(end )?feature:(!?[a-z-]+)\s*(?:-->|\*/)?\s*$`)

// applyMarkers removes, from every text file in files, the blocks of the
// features left out, and every marker line. A marker without its end, or an
// end without its start, is an error.
func applyMarkers(dir string, files []string, without []feature) error {
	out := map[string]bool{}
	for _, f := range without {
		out[f.Name] = true
	}
	for _, f := range files {
		path := filepath.Join(dir, f)
		b, err := readFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // a feature's file, gone
		}
		if err != nil {
			return err
		}
		if !strings.Contains(string(b), "feature:") || strings.IndexByte(string(b), 0) >= 0 {
			continue
		}
		s, err := stripMarkers(string(b), out)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if strings.HasSuffix(f, ".go") && s != string(b) {
			// What's left may be aligned as it was with the block in.
			formatted, err := format.Source([]byte(s))
			if err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
			s = string(formatted)
		}
		if s != string(b) {
			if err := writeFile(path, []byte(s)); err != nil {
				return err
			}
		}
	}
	return nil
}

func stripMarkers(s string, out map[string]bool) (string, error) {
	var b strings.Builder
	var open []string // the blocks we're in, innermost last
	dropping := 0     // how many of them are being left out
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if m := markerRE.FindStringSubmatch(text); m != nil {
			name := m[2]
			if m[1] == "" {
				open = append(open, name)
				// feature:!NAME is the opposite: what replaces NAME without it.
				left := out[name]
				if n, ok := strings.CutPrefix(name, "!"); ok {
					left = !out[n]
				}
				if left || dropping > 0 {
					dropping++
				}
				continue
			}
			if len(open) == 0 || open[len(open)-1] != name {
				return "", fmt.Errorf("line %d: end feature:%s without its start", line, name)
			}
			open = open[:len(open)-1]
			if dropping > 0 {
				dropping--
			}
			continue
		}
		if dropping == 0 {
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if len(open) > 0 {
		return "", fmt.Errorf("feature:%s has no end", open[len(open)-1])
	}
	if !strings.HasSuffix(s, "\n") {
		return strings.TrimSuffix(b.String(), "\n"), nil
	}
	return b.String(), nil
}

// leaveAll applies the rules of each feature left out. A rule for a file
// another of them removes is skipped: perf's lines in the release workflow,
// say, when both go.
func leaveAll(dir string, without []feature) error {
	gone := map[string]bool{}
	for _, f := range without {
		for _, p := range f.remove {
			gone[p] = true
		}
	}
	for _, f := range without {
		if err := leaveOut(dir, f, gone); err != nil {
			return err
		}
	}
	return nil
}

// goneWith reports whether file is, or is in, one of gone.
func goneWith(file string, gone map[string]bool) bool {
	for p := range gone {
		if file == p || strings.HasPrefix(file, p+"/") {
			return true
		}
	}
	return false
}

// leaveOut applies f's rules, but its markers (applyMarkers), and those for
// a file in gone.
func leaveOut(dir string, f feature, gone map[string]bool) error {
	for _, p := range f.remove {
		path := filepath.Join(dir, p)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("feature %s: %s: %w", f.Name, p, err)
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	for file, parts := range f.lines {
		if goneWith(file, gone) {
			continue
		}
		if err := edit(dir, file, func(s string) (string, error) { return dropLines(s, parts) }); err != nil {
			return fmt.Errorf("feature %s: %s: %w", f.Name, file, err)
		}
	}
	for file, pairs := range f.replace {
		if goneWith(file, gone) {
			continue
		}
		if err := edit(dir, file, func(s string) (string, error) {
			for _, p := range pairs {
				if !strings.Contains(s, p[0]) {
					return "", fmt.Errorf("no %q", p[0])
				}
				s = strings.Replace(s, p[0], p[1], 1)
			}
			return s, nil
		}); err != nil {
			return fmt.Errorf("feature %s: %s: %w", f.Name, file, err)
		}
	}
	for _, n := range f.deprecate {
		if err := deprecate(dir, n, f.Name); err != nil {
			return fmt.Errorf("feature %s: %w", f.Name, err)
		}
	}
	return nil
}

// dropLines removes each line containing one of parts, and a list item's
// continuation lines with it. Each part must match.
func dropLines(s string, parts []string) (string, error) {
	lines := strings.SplitAfter(s, "\n")
	found := make([]bool, len(parts))
	var b strings.Builder
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		k := slices.IndexFunc(parts, func(p string) bool { return strings.Contains(l, p) })
		if k < 0 {
			b.WriteString(l)
			continue
		}
		found[k] = true
		if strings.HasPrefix(strings.TrimLeft(l, " "), "- ") {
			indent := strings.Repeat(" ", len(l)-len(strings.TrimLeft(l, " "))+2)
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], indent) && strings.TrimSpace(lines[i+1]) != "" {
				i++
			}
		}
	}
	for k, ok := range found {
		if !ok {
			return "", fmt.Errorf("no line with %q", parts[k])
		}
	}
	return b.String(), nil
}

// deprecate marks decision record n Deprecated: its feature was left out.
func deprecate(dir, n, feature string) error {
	matches, err := filepath.Glob(filepath.Join(dir, "docs", "adr", n+"-*.md"))
	if err != nil || len(matches) != 1 {
		return fmt.Errorf("no decision record %s", n)
	}
	file := filepath.Join("docs", "adr", filepath.Base(matches[0]))
	return edit(dir, file, func(s string) (string, error) {
		re := regexp.MustCompile(`(?m)^- \*\*Status:\*\* .*$`)
		if !re.MatchString(s) {
			return "", errors.New("no status line")
		}
		return re.ReplaceAllString(s, "- **Status:** Deprecated: "+feature+" was left out of this service (make init)"), nil
	})
}

func edit(dir, file string, fn func(string) (string, error)) error {
	path := filepath.Join(dir, file)
	b, err := readFile(path)
	if err != nil {
		return err
	}
	s, err := fn(string(b))
	if err != nil {
		return err
	}
	return writeFile(path, []byte(s))
}
