// Package site_test checks the documentation website that `make docs` builds.
// It reads an already-built site and never builds one itself (tests don't call
// Docker), so it is skipped unless DOCS_SITE names the site's directory:
//
//	make docs-check
//
// which builds the site and then runs:
//
//	DOCS_SITE=bin/docs go test ./docs/site -count=1
package site_test

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	hrefRE  = regexp.MustCompile(`href="([^"]*)"`)
	idRE    = regexp.MustCompile(`\bid="([^"]*)"`)
	titleRE = regexp.MustCompile(`<title>([^<]+)</title>`)
)

// siteDir returns the built site's directory, or skips the test.
func siteDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("DOCS_SITE")
	if dir == "" {
		t.Skip("DOCS_SITE is not set; run make docs-check to build the site and check it")
	}
	if !filepath.IsAbs(dir) {
		// Relative to the repository root, like the make targets.
		dir = filepath.Join("..", "..", dir)
	}
	return dir
}

// expectedPages lists the pages the site must have: one per Markdown source,
// named as docs/site/build.sh names them, plus the API spec page and the
// Performance history page.
func expectedPages(t *testing.T) []string {
	t.Helper()
	pages := []string{"index.html", "changelog.html", "roadmap.html", "api.html", "history.html"}
	guides, err := filepath.Glob("../*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(guides) == 0 {
		t.Fatal("found no guides in docs/; the test is looking in the wrong place")
	}
	for _, g := range guides {
		pages = append(pages, strings.TrimSuffix(filepath.Base(g), ".md")+".html")
	}
	return pages
}

// recordPages lists the decision records' pages, one per docs/adr file,
// named like it. They're reached from the decisions page, not the
// navigation.
func recordPages(t *testing.T) []string {
	t.Helper()
	records, err := filepath.Glob("../adr/*.md")
	if err != nil || len(records) == 0 {
		t.Fatalf("no records in docs/adr: %v", err)
	}
	var pages []string
	for _, r := range records {
		pages = append(pages, strings.TrimSuffix(filepath.Base(r), ".md")+".html")
	}
	return pages
}

// TestSitePages fails if a page is missing or has no title: the build can go
// wrong quietly, for example by writing every source to the same page.
func TestSitePages(t *testing.T) {
	dir := siteDir(t)
	for _, page := range append(append(expectedPages(t), recordPages(t)...), "changes.html") {
		b, err := os.ReadFile(filepath.Join(dir, page))
		if err != nil {
			t.Errorf("missing page %s: %v", page, err)
			continue
		}
		if page == "api.html" {
			continue // Redocly's page, not ours
		}
		if m := titleRE.FindSubmatch(b); m == nil || strings.TrimSpace(string(m[1])) == "" {
			t.Errorf("%s has no <title>", page)
		}
		if !strings.Contains(string(b), `aria-current="page"`) {
			t.Errorf("%s has no navigation bar marking it as the current page", page)
		}
	}
}

// TestDecisionsIndex checks the decisions page lists every record, each
// linking to its page, as docs/site/build.sh makes it from their files.
func TestDecisionsIndex(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(siteDir(t), "decisions.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range recordPages(t) {
		if !strings.Contains(string(b), `href="`+page+`"`) {
			t.Errorf("decisions.html doesn't list %s", page)
		}
	}
}

// TestSiteLinks fails if any link on our pages points at a page or file that
// doesn't exist, at a section that doesn't exist, or still at a Markdown file.
// Links to other websites aren't followed.
func TestSiteLinks(t *testing.T) {
	dir := siteDir(t)
	pages, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil {
		t.Fatal(err)
	}

	ids := map[string]map[string]bool{} // page -> the ids in it
	content := map[string]string{}
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(p)
		content[name] = string(b)
		ids[name] = map[string]bool{}
		for _, m := range idRE.FindAllStringSubmatch(string(b), -1) {
			ids[name][html.UnescapeString(m[1])] = true
		}
	}

	checked := 0
	for name, body := range content {
		if name == "api.html" {
			continue // Redocly's page: its links are its own
		}
		for _, m := range hrefRE.FindAllStringSubmatch(body, -1) {
			href := html.UnescapeString(m[1])
			if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "mailto:") {
				continue
			}
			checked++
			target, fragment, _ := strings.Cut(href, "#")
			if target == "" {
				target = name
			}
			if strings.HasSuffix(target, ".md") {
				t.Errorf("%s links to %q, a Markdown file: docs/site/build.sh should rewrite it to .html", name, href)
				continue
			}
			targetIDs, isPage := ids[target]
			if !isPage {
				// Not one of the pages: an asset such as the stylesheet,
				// which only has to exist.
				if _, err := os.Stat(filepath.Join(dir, target)); err != nil {
					t.Errorf("%s links to %q, but there is no such page or file", name, href)
				}
				continue
			}
			if fragment != "" && target != "api.html" && !targetIDs[fragment] {
				t.Errorf("%s links to %q, but %s has no section #%s", name, href, target, fragment)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no links to check; the site is empty or the test is broken")
	}
	t.Logf("checked %d links across %d pages", checked, len(content))
}

// TestNavigationIsComplete checks every page's navigation links to every
// page, grouped under its theme, so no page is ever unreachable from another.
func TestNavigationIsComplete(t *testing.T) {
	dir := siteDir(t)
	pages := expectedPages(t)
	navRE := regexp.MustCompile(`(?s)<nav class="site-nav">(.*?)</nav>`)
	for _, page := range pages {
		if page == "api.html" {
			continue // Redocly's page, without our navigation
		}
		b, err := os.ReadFile(filepath.Join(dir, page))
		if err != nil {
			continue // TestSitePages reports it
		}
		m := navRE.FindSubmatch(b)
		if m == nil {
			t.Errorf("%s has no navigation", page)
			continue
		}
		nav := string(m[1])
		if n := strings.Count(nav, `class="nav-theme"`); n < 2 {
			t.Errorf("%s's navigation has %d themes; want the pages grouped", page, n)
		}
		for _, other := range pages {
			if !strings.Contains(nav, `href="`+other+`"`) {
				t.Errorf("%s's navigation has no link to %s", page, other)
			}
		}
	}
}

// TestReadmeGroupsLikeTheSite checks the README's list of documents uses the
// site's themes, in the same order, each listing the same pages, so the two
// ways in to the documentation agree.
func TestReadmeGroupsLikeTheSite(t *testing.T) {
	script, err := os.ReadFile("build.sh")
	if err != nil {
		t.Fatal(err)
	}
	type theme struct {
		name    string
		sources []string
	}
	var site []theme
	for _, m := range regexp.MustCompile(`(?m)^([A-Z][^|\n]*)\|([^|\n]+)\|[^|\n]+$`).FindAllStringSubmatch(string(script), -1) {
		if len(site) == 0 || site[len(site)-1].name != m[1] {
			site = append(site, theme{name: m[1]})
		}
		src := m[2]
		switch src {
		case "README.md":
			continue // the README is the Overview: it doesn't list itself
		case "-":
			src = "docs/openapi.json" // the spec, which the site renders as api.html
		case "+history":
			continue // generated from the perf-data branch: no file to link to
		}
		site[len(site)-1].sources = append(site[len(site)-1].sources, src)
	}
	if len(site) < 2 {
		t.Fatal("found no themes in build.sh's navigation")
	}

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(readme)
	doc = doc[strings.Index(doc, "\n## Documentation"):]
	doc = doc[:strings.Index(doc[1:], "\n## ")+1]
	var got []theme
	for line := range strings.SplitSeq(doc, "\n") {
		if m := regexp.MustCompile(`^\*\*(.+)\*\*$`).FindStringSubmatch(line); m != nil {
			got = append(got, theme{name: m[1]})
		} else if m := regexp.MustCompile(`^- \[[^\]]+\]\(([^)]+)\)`).FindStringSubmatch(line); m != nil && len(got) > 0 {
			got[len(got)-1].sources = append(got[len(got)-1].sources, m[1])
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(site) {
		t.Errorf("the README's Documentation list doesn't match the site's themes:\n README: %v\n site:   %v", got, site)
	}
}

// TestHistoryPage checks the Performance history page holds either the
// charts, as live SVG (a blank line in the fragment, or a change in pandoc,
// would turn them into escaped text), or the note saying where to find them.
func TestHistoryPage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(siteDir(t), "history.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if strings.Contains(page, "&lt;svg") || strings.Contains(page, "&lt;section") {
		t.Fatal("history.html shows the charts' HTML as text")
	}
	charted := strings.Contains(page, `<div class="perf-history">`)
	// Every paragraph the charts have carries a class; a bare one is pandoc
	// wrapping text it no longer took for raw HTML, inside a chart.
	if i := strings.Index(page, `<div class="perf-history">`); charted && strings.Contains(page[i:], "<p>") {
		t.Error("history.html has a bare <p> among the charts: pandoc broke the raw HTML up")
	}
	switch {
	case charted && strings.Count(page, "<svg") == 0:
		t.Error("history.html has the charts' wrapper but no chart")
	case !charted && !strings.Contains(page, "This build has no performance history"):
		t.Error("history.html has neither the charts nor the note saying where they are")
	}
}
