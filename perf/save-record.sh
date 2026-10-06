#!/bin/sh
# Saves a record to the perf-data branch, the history that `make perf-trends`
# charts. The workflows that measure main run it: perf-trends.yml for the
# performance records, ci-times.yml for the CI timings.
#
#   sh perf/save-record.sh <dir> <record.json> <commit message>
#
# The record is saved as <dir>/<UTC time>-<short sha>.json, so a directory
# sorts oldest first. Run it from a checkout of main, with git's user set; it
# creates the branch if there isn't one yet. RECORD_SHA names the commit the
# record is for, GITHUB_SHA if it's unset: in a workflow_run workflow, that's
# main's latest commit, not the run's.
set -eu

dir=${1:?usage: save-record.sh <dir> <record.json> <message>}
file=${2:?usage: save-record.sh <dir> <record.json> <message>}
message=${3:?usage: save-record.sh <dir> <record.json> <message>}
sha=${RECORD_SHA:-${GITHUB_SHA:?set RECORD_SHA to the commit the record is for}}

data=$(mktemp -d)
# Leave nothing behind: a registered worktree would hold perf-data checked
# out, and the next save from this checkout couldn't check it out again.
repo=$(pwd)
trap 'git -C "$repo" worktree remove --force "$data" 2>/dev/null || rm -rf "$data"' EXIT
if git ls-remote --exit-code --heads origin perf-data >/dev/null; then
	git fetch -q --depth 1 origin perf-data
	git worktree add -q -B perf-data "$data" FETCH_HEAD
else
	git worktree add -q --orphan -b perf-data "$data"
	printf '%s\n' "# Performance records" "" \
		"records/ holds one JSON file per commit on main, written by .github/workflows/perf-trends.yml;" \
		"ci/ one per successful CI run on main, written by .github/workflows/ci-times.yml." \
		"Run \`make perf-trends\` on main to chart them." >"$data/README.md"
fi
mkdir -p "$data/$dir"
cp "$file" "$data/$dir/$(date -u +%Y%m%dT%H%M%S)-$(echo "$sha" | cut -c1-7).json"
cd "$data"
git add -A
git commit -q -m "$message"
# Another record may have landed meanwhile: rebase onto it and retry.
for attempt in 1 2 3; do
	git push -q origin HEAD:perf-data && exit 0
	echo "save-record: push $attempt failed; rebasing onto the branch's latest" >&2
	git fetch -q origin perf-data && git rebase -q FETCH_HEAD
done
exit 1
