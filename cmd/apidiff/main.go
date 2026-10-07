// Command apidiff compares two versions of the API's OpenAPI spec and says
// what changed for clients: what's breaking (a client written against the
// old spec may stop working) and what's additive. scripts/api-compat.sh
// runs it against the last release's spec; see docs/releasing.md.
//
//	go run ./cmd/apidiff -base old.json -head docs/openapi.json [-from v0.6.0 -to v0.7.0] [-changelog CHANGELOG.md]
//
// It prints the changes as Markdown and exits 1 when a check fails:
//
//   - with -from and -to, when the version bump is too small for what changed
//     (see RequiredBump): a release's check;
//   - with -changelog, when there's a breaking change and the changelog's
//     Unreleased section doesn't say so with a "**Breaking**" entry: a pull
//     request's check, so a breaking change is always a decision, written
//     down for the release notes.
//
// Only what clients see is compared: operations, parameters, bodies,
// statuses, response headers and schemas. Descriptions aren't.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		var failed checkFailed
		if errors.As(err, &failed) {
			fmt.Fprintln(os.Stderr, "apidiff:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "apidiff:", err)
		os.Exit(2)
	}
}

// checkFailed is a check's verdict, not a failure to run it.
type checkFailed struct{ msg string }

func (e checkFailed) Error() string { return e.msg }

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("apidiff", flag.ContinueOnError)
	base := fs.String("base", "", "the old spec, e.g. the last release's")
	head := fs.String("head", "docs/openapi.json", "the new spec")
	from := fs.String("from", "", "the old spec's version, vX.Y.Z, to check the bump to -to")
	to := fs.String("to", "", "the version being released, vX.Y.Z")
	changelog := fs.String("changelog", "", "require a **Breaking** entry in this changelog's Unreleased section for a breaking change")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" {
		return errors.New("-base is required")
	}
	if (*from == "") != (*to == "") {
		return errors.New("-from and -to go together")
	}
	oldDoc, err := load(*base)
	if err != nil {
		return err
	}
	newDoc, err := load(*head)
	if err != nil {
		return err
	}
	changes := Diff(oldDoc, newDoc)
	report(out, changes)

	breaking := 0
	for _, c := range changes {
		if c.Kind == Breaking {
			breaking++
		}
	}
	if *from != "" {
		f, err := ParseVersion(*from)
		if err != nil {
			return err
		}
		t, err := ParseVersion(*to)
		if err != nil {
			return err
		}
		bump, required := Bump(f, t), RequiredBump(changes, f)
		fmt.Fprintf(out, "\n%s → %s is a %s release; these changes need at least %s.\n", *from, *to, orNone(bump), required)
		if !Enough(bump, required) {
			return checkFailed{fmt.Sprintf("%s → %s: the changes since %s need a %s release, at least", *from, *to, *from, required)}
		}
	}
	if *changelog != "" && breaking > 0 {
		b, err := os.ReadFile(*changelog)
		if err != nil {
			return err
		}
		if !strings.Contains(unreleased(string(b)), "**Breaking**") {
			return checkFailed{fmt.Sprintf("%d breaking API changes, and %s's Unreleased section has no **Breaking** entry: if they're intended, say what breaks there, for the release notes", breaking, *changelog)}
		}
	}
	return nil
}

func load(path string) (map[string]any, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the spec named on the command line, by whoever runs the tool
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// report writes the changes as Markdown. The same change in many places,
// such as a new error code in every operation's error responses, is one
// line, with how many places and a few of them.
func report(w io.Writer, changes []Change) {
	if len(changes) == 0 {
		fmt.Fprintln(w, "No API changes clients can see.")
		return
	}
	for _, kind := range []Kind{Breaking, Additive} {
		type group struct {
			field, what string
			places      []string
		}
		var groups []*group
		byKey := map[string]*group{}
		for _, c := range changes {
			if c.Kind != kind {
				continue
			}
			// "GET /items: response 400: body.code" is the field
			// "body.code" in the place "GET /items: response 400".
			place, field := c.Where, ""
			if i := strings.LastIndex(c.Where, ": "); i >= 0 {
				place, field = c.Where[:i], c.Where[i+2:]
			}
			key := field + "\x00" + c.What
			g := byKey[key]
			if g == nil {
				g = &group{field: field, what: c.What}
				byKey[key] = g
				groups = append(groups, g)
			}
			g.places = append(g.places, place)
		}
		if len(groups) == 0 {
			continue
		}
		title := map[Kind]string{Breaking: "Breaking changes", Additive: "Additive changes"}[kind]
		fmt.Fprintf(w, "### %s\n\n", title)
		for _, g := range groups {
			switch {
			case len(g.places) == 1 && g.field == "":
				fmt.Fprintf(w, "- `%s`: %s\n", g.places[0], g.what)
			case len(g.places) == 1:
				fmt.Fprintf(w, "- `%s: %s`: %s\n", g.places[0], g.field, g.what)
			default:
				shown := g.places[:min(3, len(g.places))]
				more := ""
				if len(g.places) > len(shown) {
					more = fmt.Sprintf(", and %d more", len(g.places)-len(shown))
				}
				fmt.Fprintf(w, "- `%s`: %s, in %d places: `%s`%s\n", g.field, g.what, len(g.places), strings.Join(shown, "`, `"), more)
			}
		}
		fmt.Fprintln(w)
	}
}

// unreleased is the changelog's Unreleased section.
func unreleased(changelog string) string {
	_, rest, ok := strings.Cut(changelog, "\n## Unreleased\n")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func orNone(bump string) string {
	if bump == "" {
		return "not a later"
	}
	return bump
}
