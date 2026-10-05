#!/bin/sh
# Works on CHANGELOG.md for releases; see docs/releasing.md.
#
#   sh scripts/changelog.sh cut vX.Y.Z    turn Unreleased into the release vX.Y.Z,
#                                         dated today, under a new empty Unreleased
#   sh scripts/changelog.sh notes vX.Y.Z  print vX.Y.Z's section, for the GitHub release
#   sh scripts/changelog.sh notes         print Unreleased (what the next release holds)
#
# The changelog follows Keep a Changelog: a release is "## [X.Y.Z] - YYYY-MM-DD".
# CHANGELOG (default CHANGELOG.md) names the file.
set -eu

file=${CHANGELOG:-CHANGELOG.md}
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

case $cmd in
notes)
	if [ -z "$version" ]; then
		section "## Unreleased" | sed '/./,$!d'
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
	if [ -z "$(section '## Unreleased' | tr -d '[:space:]')" ]; then
		echo "changelog: Unreleased is empty: nothing to release" >&2
		exit 1
	fi
	tmp=$(mktemp)
	awk -v rel="## [$number] - $(date -u +%Y-%m-%d)" '
		$0 == "## Unreleased" && !done { print; print ""; print rel; done = 1; next }
		{ print }
	' "$file" >"$tmp"
	mv "$tmp" "$file"
	echo "changelog: Unreleased is now $version"
	;;
*)
	echo "usage: changelog.sh cut|notes [vX.Y.Z]" >&2
	exit 2
	;;
esac
