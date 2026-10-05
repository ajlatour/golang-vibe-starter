#!/bin/sh
# Runs one k6 profile from perf/profiles.json against the running stack, with
# k6 in Docker on the stack's network. `make perf PROFILE=<name>` starts the
# stack and calls this; see docs/performance.md.
#
#   sh perf/run.sh <profile> [summary file name]
#
# k6 prints its summary, with each threshold marked as passed or crossed, and
# the summary is also saved as JSON in bin/perf/. The exit status is k6's: 0
# when every threshold held, 99 when one was crossed. For a profile marked
# "informational" (stress), crossed thresholds are what the test is looking
# for, so 99 becomes 0.
set -eu

profile=${1:?usage: run.sh <profile>}
out=bin/perf
mkdir -p "$out"
summary=${2:-$profile-$(date +%Y%m%d-%H%M%S).json}

# informational reports whether the profile has "informational": true: the
# text of its object, from its key to the matching closing brace, is checked
# for that key. The profile descriptions contain no braces (a test checks).
informational() {
	awk -v key="\"$1\"" '
		!found && index($0, key) && $0 ~ /: *\{/ { found = 1 }
		found {
			text = text $0
			depth += gsub(/\{/, "{") - gsub(/\}/, "}")
			if (depth == 0) exit
		}
		END { exit !(text ~ /"informational" *: *true/) }
	' perf/profiles.json
}

# Off a terminal (CI, or a file), skip the progress lines k6 prints every
# second; the summary still prints.
quiet=
[ -t 1 ] || quiet=--quiet

status=0
# As your user, so the summary in bin/perf isn't owned by root or k6's user.
docker compose --profile perf run --rm --user "$(id -u):$(id -g)" \
	-e PROFILE="$profile" k6 run $quiet --summary-export "/out/$summary" /perf/k6.js || status=$?

echo "summary: $out/$summary"
if [ "$status" -eq 99 ] && informational "$profile"; then
	echo "Thresholds crossed, as a $profile test intends: the last phase whose thresholds all held is"
	echo "the most this setup handles within budget."
	exit 0
fi
exit "$status"
