#!/bin/sh
# Compares docs/openapi.json with the last release's, to say what changed for
# clients and check it's handled (cmd/apidiff; see docs/releasing.md).
#
#   sh scripts/api-compat.sh pr                a pull request's check: report the
#                                              changes, and require a **Breaking**
#                                              entry in the changelog's Unreleased
#                                              section for a breaking one
#   sh scripts/api-compat.sh release vX.Y.Z    a release's check: the bump from the
#                                              last release to vX.Y.Z must cover the
#                                              changes (0.x: minor for a breaking
#                                              one; from 1.0, major)
#
# The last release is the latest version tag reachable from HEAD, other than
# the one being released; it needs the tags fetched. With none yet, there's
# nothing to compare with. The report also goes to the job summary in CI.
set -eu

mode=${1:?usage: api-compat.sh pr | release vX.Y.Z}
version=${2:-}
[ "$mode" = pr ] || [ "$mode" = release ] || { echo "usage: api-compat.sh pr | release vX.Y.Z" >&2; exit 2; }
[ "$mode" = pr ] || [ -n "$version" ] || { echo "usage: api-compat.sh release vX.Y.Z" >&2; exit 2; }

if ! last=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude "${version:-none}" HEAD 2>/dev/null); then
	echo "api-compat: no earlier release tag to compare with; nothing to check."
	exit 0
fi
base=$(mktemp)
report=$(mktemp)
trap 'rm -f "$base" "$report"' EXIT
git show "$last:docs/openapi.json" >"$base"

status=0
if [ "$mode" = pr ]; then
	go run ./cmd/apidiff -base "$base" -head docs/openapi.json -changelog CHANGELOG.md >"$report" || status=$?
else
	go run ./cmd/apidiff -base "$base" -head docs/openapi.json -from "$last" -to "$version" >"$report" || status=$?
fi
{
	echo "## API changes since $last"
	echo
	cat "$report"
} | tee -a "${GITHUB_STEP_SUMMARY:-/dev/null}"
exit "$status"
