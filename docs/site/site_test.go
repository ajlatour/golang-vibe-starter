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
// named as docs/site/build.sh names them, plus the API spec page.
func expectedPages(t *testing.T) []string {
	t.Helper()
	pages := []string{"index.html", "changelog.html", "api.html"}
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

// TestSitePages fails if a page is missing or has no title: the build can go
// wrong quietly, for example by writing every source to the same page.
func TestSitePages(t *testing.T) {
	dir := siteDir(t)
	for _, page := range expectedPages(t) {
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
