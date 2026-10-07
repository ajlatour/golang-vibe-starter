#!/bin/sh
# Works on CHANGELOG.md for releases; see docs/releasing.md.
#
#   sh scripts/changelog.sh cut vX.Y.Z    make the release vX.Y.Z, dated today, from what's
#                                         unreleased: the files in changes/ (and anything
#                                         under Unreleased), which it then removes
#   sh scripts/changelog.sh notes vX.Y.Z  print vX.Y.Z's section, for the GitHub release
#   sh scripts/changelog.sh notes         print what the next release holds
#
# The changelog follows Keep a Changelog: a release is "## [X.Y.Z] - YYYY-MM-DD".
# What's unreleased lives in changes/, one file per change, each with Keep a
# Changelog's sections ("### Added", "### Fixed", ...), so pull requests don't
# all edit one section of one file and conflict. A release gathers them into
# one section, each heading once, in Keep a Changelog's order, the files'
# entries in the order of their names. CHANGELOG (default CHANGELOG.md) and
# CHANGES (default changes) name the file and the directory.
set -eu

file=${CHANGELOG:-CHANGELOG.md}
changes=${CHANGES:-changes}
cmd=${1:?usage: changelog.sh cut|notes [vX.Y.Z]}
version=${2:-}

if [ -n "$version" ] && ! echo "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "changelog: version $version: want vMAJOR.MINOR.PATCH, e.g. v0.1.0" >&2
	exit 2
fi
number=${version#v}

# section prints the body of the section whose heading line is $1, up to the
# next "## " heading.
section() {
	awk -v head="$1" '
		$0 == head { on = 1; next }
		on && /^## / { exit }
		on { print }
	' "$file"
}

# heading_of prints the heading line of release $1, if there is one.
heading_of() {
	grep -E "^## \[$(echo "$1" | sed 's/\./\\./g')\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" "$file" | head -n 1
}

# fragments prints the change files, one per line, in name order: every
# Markdown file in changes/ but its README.
fragments() {
	[ -d "$changes" ] || return 0
	for f in "$changes"/*.md; do
		[ -f "$f" ] && [ "$(basename "$f")" != README.md ] && echo "$f"
	done
	return 0
}

# unreleased prints what's unreleased, gathered: the Unreleased section's
# entries and every change file's, under each heading once, in Keep a
# Changelog's order. It fails on a change file that isn't all entries under
# those headings, naming it.
unreleased() {
	tmp=$(mktemp)
	section "## Unreleased" >"$tmp"
	set -- "$tmp"
	for f in $(fragments); do set -- "$@" "$f"; done
	status=0
	for kind in Added Changed Deprecated Removed Fixed Security; do
		entries=$(awk -v want="### $kind" -v tmp="$tmp" '
			FNR == 1 { on = 0 }
			/^### / {
				if ($0 !~ /^### (Added|Changed|Deprecated|Removed|Fixed|Security)$/) {
					printf "changelog: %s: %s: want one of Added, Changed, Deprecated, Removed, Fixed, Security\n", (FILENAME == tmp ? "Unreleased" : FILENAME), $0 > "/dev/stderr"
					exit 1
				}
				on = ($0 == want)
				next
			}
			/^[[:space:]]*$/ { next }
			# A change file links from changes/; the changelog, from the root.
			on && FILENAME != tmp { gsub(/\]\(\.\.\//, "](") }
			on { print }
		' "$@") || { status=1; break; }
		if [ -n "$entries" ]; then
			printf '### %s\n\n%s\n\n' "$kind" "$entries"
		fi
	done
	# An entry outside any heading would be lost: refuse it.
	for f in "$@"; do
		stray=$(awk '/^### /{exit} /[^[:space:]]/{print; exit}' "$f")
		if [ -n "$stray" ]; then
			name=$f
			[ "$f" = "$tmp" ] && name=Unreleased
			echo "changelog: $name: \"$stray\" isn't under a ### heading" >&2
			status=1
		fi
	done
	rm -f "$tmp"
	return "$status"
}

case $cmd in
notes)
	if [ -z "$version" ]; then
		unreleased
		exit 0
	fi
	head=$(heading_of "$number")
	if [ -z "$head" ]; then
		echo "changelog: $file has no section for $version (## [$number] - <date>)" >&2
		exit 1
	fi
	section "$head" | sed '/./,$!d'
	;;
cut)
	[ -n "$version" ] || { echo "usage: changelog.sh cut vX.Y.Z" >&2; exit 2; }
	if [ -n "$(heading_of "$number")" ]; then
		echo "changelog: $version is already released in $file" >&2
		exit 1
	fi
	if ! grep -qx '## Unreleased' "$file"; then
		echo "changelog: $file has no '## Unreleased' section" >&2
		exit 1
	fi
	body=$(mktemp)
	unreleased >"$body"
	if [ -z "$(tr -d '[:space:]' <"$body")" ]; then
		rm -f "$body"
		echo "changelog: nothing unreleased in $changes/ or under Unreleased: nothing to release" >&2
		exit 1
	fi
	# The release goes under an emptied Unreleased.
	tmp=$(mktemp)
	awk -v rel="## [$number] - $(date -u +%Y-%m-%d)" -v body="$body" '
		$0 == "## Unreleased" && !done {
			print; print ""; print rel; print ""
			while ((getline line < body) > 0) print line
			done = 1; skip = 1; next
		}
		skip && /^## / { skip = 0 }
		skip { next }
		{ print }
	' "$file" >"$tmp"
	mv "$tmp" "$file"
	rm -f "$body"
	for f in $(fragments); do rm -f "$f"; done
	echo "changelog: Unreleased is now $version"
	;;
*)
	echo "usage: changelog.sh cut|notes [vX.Y.Z]" >&2
	exit 2
	;;
esac
