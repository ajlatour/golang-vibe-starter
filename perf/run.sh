#!/bin/sh
# Runs one k6 profile from perf/profiles.json against the running stack, with
# k6 in Docker on the stack's network. `make perf PROFILE=<name>` starts the
# stack and calls this; see docs/performance.md.
#
#   sh perf/run.sh <profile> [summary file name]
#
# k6 prints its summary, with each threshold marked as passed or crossed, and
# the summary is also saved as JSON in bin/perf/. perf/resources.sh records
# what the run cost in CPU and memory alongside it, and prints a summary. When
# the monitoring stack is up (make monitor), k6 also sends its results to
# Prometheus, for the Grafana dashboard. The exit status is k6's: 0
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

# With the monitoring stack running, k6 writes its metrics to Prometheus too,
# as they happen, with the percentiles the dashboard plots.
output=
if [ -n "$(docker compose --profile monitoring ps --status running -q prometheus 2>/dev/null)" ]; then
	output="-o experimental-prometheus-rw"
fi

stem=$out/${summary%.json}
sh perf/resources.sh start "$stem"
status=0
# As your user, so the summary in bin/perf isn't owned by root or k6's user.
# --no-deps: against the stack as it is. Otherwise compose would recreate the
# API to match compose.yaml's defaults, replacing the image load-ab.sh chose.
# $quiet and $output are each an option or nothing, so they're split, not quoted.
# shellcheck disable=SC2086
docker compose --profile perf run --rm --no-deps --user "$(id -u):$(id -g)" \
	-e PROFILE="$profile" \
	-e K6_PROMETHEUS_RW_SERVER_URL=http://prometheus:9090/api/v1/write \
	-e 'K6_PROMETHEUS_RW_TREND_STATS=p(50),p(99),max' \
	-e K6_PROMETHEUS_RW_PUSH_INTERVAL=2s \
	k6 run $quiet $output --summary-export "/out/$summary" /perf/k6.js || status=$?
sh perf/resources.sh stop "$stem"

echo "summary: $out/$summary"
if [ "$status" -eq 99 ] && informational "$profile"; then
	echo "Thresholds crossed, as a $profile test intends: the last phase whose thresholds all held is"
	echo "the most this setup handles within budget."
	exit 0
fi
exit "$status"
