#!/bin/sh
# Sets the OpenAPI spec's info.version to a release's version; see
# docs/releasing.md. make release-prepare runs it alongside changelog.sh cut,
# so the spec always names the latest release.
#
#   sh scripts/spec-version.sh vX.Y.Z
#
# SPEC (default docs/openapi.json) names the file. Only the info object's
# "version" line changes, so the rest of the file keeps its formatting.
set -eu

file=${SPEC:-docs/openapi.json}
version=${1:?usage: spec-version.sh vX.Y.Z}

if ! echo "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "spec-version: version $version: want vMAJOR.MINOR.PATCH, e.g. v0.1.0" >&2
	exit 2
fi

# The info object opens the file and holds its first "version"; one further
# in (a schema's property, say) is never touched.
tmp=$(mktemp)
awk -v number="${version#v}" '
	/^  "info": \{$/ { info = 1 }
	info && /^  \}/ { info = 0 }
	info && !done && /^    "version": "[^"]*",$/ {
		print "    \"version\": \"" number "\","
		done = 1
		next
	}
	{ print }
	END { if (!done) exit 1 }
' "$file" >"$tmp" || {
	rm -f "$tmp"
	echo "spec-version: $file has no info.version line to set" >&2
	exit 1
}
mv "$tmp" "$file"
echo "spec-version: $file is now version ${version#v}"
