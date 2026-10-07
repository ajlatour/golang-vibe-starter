#!/bin/sh
# Renders the README, the guides in docs/ and the CHANGELOG into a small static
# site of HTML pages. `make docs` runs it inside the pandoc/core Docker image,
# from the repository root; the API spec page (api.html) is rendered
# separately, with Redocly.
#
#   sh docs/site/build.sh <output directory> [performance history]
#
# The Performance history page holds the charts of main's performance and CI
# times when given them, as an HTML fragment (`report trends -fragment`; make
# docs-release, for the docs a release attaches); without, it says where to
# find them.
#
# Pages work straight from disk: links between documents are rewritten from
# .md to .html, the link to docs/openapi.json points at api.html, the one to
# .env.example at a copy of it, and the CI
# badge becomes a plain link (GitHub serves a private repo's badge only to a
# signed-in browser, so it would show as a broken image).
set -eu

out=${1:?usage: build.sh <output directory> [performance history]}
history=${2:-}
mkdir -p "$out"
cp docs/site/site.css "$out/site.css"
# Linked from the README; .txt so a browser shows it rather than downloading it.
cp .env.example "$out/env.example.txt"

# The navigation, by theme: one "theme|source|output" line per page, in
# order. "-" as the source is a page built elsewhere (Redocly's API spec), and
# "+history" the Performance history page, which this script writes.
# Every guide in docs/ must be listed: the build stops on one that isn't, so a
# new guide is placed in a theme on purpose.
navigation='Start|README.md|index.html
Start|CHANGELOG.md|changelog.html
Start|ROADMAP.md|roadmap.html
Use the API|docs/curl.md|curl.html
Use the API|-|api.html
Run it|docs/local-stack.md|local-stack.html
Run it|docs/feature-flags.md|feature-flags.html
Run it|docs/request-validation.md|request-validation.html
Run it|docs/logging.md|logging.html
Build and ship|docs/development.md|development.html
Build and ship|docs/performance.md|performance.html
Build and ship|+history|history.html
Build and ship|docs/releasing.md|releasing.html
Architecture|docs/architecture.md|architecture.html
Architecture|docs/decisions.md|decisions.html'

for guide in docs/*.md; do
	if ! echo "$navigation" | grep -q "|$guide|"; then
		echo "build.sh: $guide isn't in the navigation; add it to a theme in docs/site/build.sh" >&2
		exit 1
	fi
done

# The pages this script renders, as source:output: those in the navigation,
# and a page for each decision record (docs/adr), named like its file,
# reached from the decisions page.
pages=$(echo "$navigation" | awk -F'|' '$2 != "-" { printf "%s:%s ", $2, $3 }')
for record in docs/adr/*.md; do
	pages="$pages$record:$(basename "$record" .md).html "
done
# How to write a change file, reached from the releasing guide.
pages="${pages}changes/README.md:changes.html "

# The decisions page's source: docs/decisions.md, and an index of the
# records, made from their files: each one's title and status. The index
# isn't kept in the Markdown, so records added at the same time don't
# conflict there.
decisions_md="$out/.decisions.md"
{
	cat docs/decisions.md
	echo
	echo "| Record | Decision | Status |"
	echo "| ------ | -------- | ------ |"
	for record in docs/adr/*.md; do
		heading=$(sed -n '1s/^# //p' "$record")
		status=$(sed -n 's/^- \*\*Status:\*\* //p' "$record" | head -n 1)
		echo "| [${heading%%:*}](adr/$(basename "$record")) | ${heading#*: } | $status |"
	done
} > "$decisions_md"

# The Performance history page's source: the charts, a raw HTML block with no
# blank line (one would end it), or where to find them.
history_md="$out/.history.md"
{
	echo "# Performance history"
	echo
	if [ -n "$history" ]; then
		echo "How \`main\`'s performance, and CI's time, changed up to this release: the records on the"
		echo "\`perf-data\` branch, charted ([Performance](docs/performance.md#trends) explains them)."
		echo
		cat "$history"
	else
		echo "This build has no performance history. The documentation attached to each"
		echo "[release](docs/releasing.md#what-the-tag-publishes) includes it, charting \`main\`'s performance and"
		echo "CI's time up to that release; \`make docs-release\` builds it here, and \`make perf-trends\` charts"
		echo "the latest ([Performance](docs/performance.md#trends))."
	fi
} > "$history_md"

# A page's title is its first top-level heading.
title() { sed -n 's/^# //p' "$1" | head -n 1; }

# The navigation bar, a group per theme, with the current page marked. POSIX
# sh has no local variables, so these names must not be reused by the main
# loop below.
nav() {
	printf '<nav class="site-nav">\n'
	nav_theme=
	echo "$navigation" | {
		while IFS='|' read -r theme nav_src nav_dest; do
			if [ "$theme" != "$nav_theme" ]; then
				[ -n "$nav_theme" ] && printf '  </div>\n'
				printf '  <div class="nav-group"><span class="nav-theme">%s</span>\n' "$theme"
				nav_theme=$theme
			fi
			case $nav_src in
			-) nav_label="API spec" ;;
			+history) nav_label=$(title "$history_md") ;;
			README.md) nav_label=Overview ;;
			*) nav_label=$(title "$nav_src") ;;
			esac
			if [ "$nav_dest" = "$1" ]; then
				printf '    <a href="%s" aria-current="page">%s</a>\n' "$nav_dest" "$nav_label"
			else
				printf '    <a href="%s">%s</a>\n' "$nav_dest" "$nav_label"
			fi
		done
		printf '  </div>\n'
	}
	printf '</nav>\n'
}

for page in $pages; do
	src=${page%%:*} dest=${page#*:}
	[ "$src" = +history ] && src=$history_md
	[ "$src" = docs/decisions.md ] && src=$decisions_md
	case $src in
	docs/adr/*) nav decisions.html ;; # a record is part of the decisions
	changes/*) nav releasing.html ;;
	*) nav "$dest" ;;
	esac > "$out/.nav.html"
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
			-e 's#href="(\.\./)?ROADMAP\.md#href="roadmap.html#g' \
			-e 's#href="adr/"#href="decisions.html\#the-records"#g' \
			-e 's#href="(\.\./)?changes/README\.md#href="changes.html#g' \
			-e 's#href="(docs/)?adr/([^"/]+)\.md#href="\2.html#g' \
			-e 's#href="\.\./([^":/]+)\.md#href="\1.html#g' \
			-e 's#href="docs/([^"/]+)\.md#href="\1.html#g' \
			-e 's#href="([^":/]+)\.md#href="\1.html#g' \
			-e 's#<img src="[^"]*/badge\.svg"[^>]*>#CI status#g' \
		> "$out/$dest"
	echo "  $src -> $out/$dest"
done
rm -f "$out/.nav.html" "$history_md" "$decisions_md"
