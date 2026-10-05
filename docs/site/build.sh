#!/bin/sh
# Renders the README, the guides in docs/ and the CHANGELOG into a small static
# site of HTML pages. `make docs` runs it inside the pandoc/core Docker image,
# from the repository root; the API spec page (api.html) is rendered
# separately, with Redocly.
#
#   sh docs/site/build.sh <output directory>
#
# Pages work straight from disk: links between documents are rewritten from
# .md to .html, the link to docs/openapi.json points at api.html, the one to
# .env.example at a copy of it, and the CI
# badge becomes a plain link (GitHub serves a private repo's badge only to a
# signed-in browser, so it would show as a broken image).
set -eu

out=${1:?usage: build.sh <output directory>}
mkdir -p "$out"
cp docs/site/site.css "$out/site.css"
# Linked from the README; .txt so a browser shows it rather than downloading it.
cp .env.example "$out/env.example.txt"

# Every page, as source:output, in navigation order. Guides added to docs/ are
# picked up automatically.
pages="README.md:index.html"
for guide in docs/*.md; do
	pages="$pages $guide:$(basename "$guide" .md).html"
done
pages="$pages CHANGELOG.md:changelog.html"

# A page's title is its first top-level heading.
title() { sed -n 's/^# //p' "$1" | head -n 1; }

# The navigation bar, with the current page marked. POSIX sh has no local
# variables, so these names must not be reused by the main loop below.
nav() {
	printf '<nav class="site-nav">\n'
	for nav_page in $pages; do
		nav_src=${nav_page%%:*} nav_dest=${nav_page#*:}
		nav_label=$(title "$nav_src")
		[ "$nav_src" = README.md ] && nav_label=Overview
		if [ "$nav_dest" = "$1" ]; then
			printf '  <a href="%s" aria-current="page">%s</a>\n' "$nav_dest" "$nav_label"
		else
			printf '  <a href="%s">%s</a>\n' "$nav_dest" "$nav_label"
		fi
	done
	printf '  <a href="api.html">API spec</a>\n</nav>\n'
}

for page in $pages; do
	src=${page%%:*} dest=${page#*:}
	nav "$dest" > "$out/.nav.html"
	# --wrap=none keeps each element on one line, so the sed rewrites below
	# can rely on matching it.
	pandoc "$src" --from gfm --to html5 --standalone --no-highlight --wrap=none \
		--metadata pagetitle="$(title "$src") · golang-vibe-starter" \
		--css site.css --include-before-body "$out/.nav.html" \
		| sed -E \
			-e 's#href="(\.\./)?docs/openapi\.json"#href="api.html"#g' \
			-e 's#href="openapi\.json"#href="api.html"#g' \
			-e 's#href="(\.\./)?\.env\.example"#href="env.example.txt"#g' \
			-e 's#href="(\.\./)?README\.md#href="index.html#g' \
			-e 's#href="(\.\./)?CHANGELOG\.md#href="changelog.html#g' \
			-e 's#href="docs/([^"/]+)\.md#href="\1.html#g' \
			-e 's#href="([^":/]+)\.md#href="\1.html#g' \
			-e 's#<img src="[^"]*/badge\.svg"[^>]*>#CI status#g' \
		> "$out/$dest"
	echo "  $src -> $out/$dest"
done
rm -f "$out/.nav.html"
