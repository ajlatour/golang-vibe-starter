#!/bin/sh
# Runs a k6 profile against two versions of the API, the working tree and a
# base commit, in turn on the same stack. `make load-compare` and the perf
# workflow call it; see docs/performance.md.
#
#   sh perf/load-ab.sh <base ref> <rounds> <output dir> [profile]
#
# A base ref of - measures the working tree alone (the perf-trends workflow
# records main this way).
# Builds an API image for each version, then for each round runs the profile
# (default compare) against each, alternating which goes first, with the same
# stubs throughout. A first run against the working tree warms
# up the stack (WireMock's JIT above all) and is thrown away. Each run leaves
# <output dir>/<side>-<n>.json (k6's summary), .txt (k6's output), and what
# perf/resources.sh recorded: .stats.tsv and .metrics-{start,end}.prom.
#
# Exits 1 if any run against the working tree crossed the profile's
# thresholds, after every run is done; the base's results are only compared.
set -eu

base=${1:?usage: load-ab.sh <base ref> <rounds> <output dir> [profile]}
rounds=${2:?usage: load-ab.sh <base ref> <rounds> <output dir> [profile]}
out=${3:?usage: load-ab.sh <base ref> <rounds> <output dir> [profile]}
profile=${4:-compare}
mkdir -p "$out"
basesrc=$(cd "$out" && pwd)/base-src

cleanup() { git worktree remove --force "$basesrc" 2>/dev/null || true; }
trap cleanup EXIT
cleanup
if [ "$base" != - ]; then
	git worktree add --quiet --detach "$basesrc" "$base"
fi

echo "Starting the stubs, and building the API at $base and in the working tree..."
# All at once: the two builds share BuildKit's base images and run their Go
# builds side by side, while the other containers start.
stubs=mock
# feature:flags
stubs="mock flagd"
# end feature:flags
# shellcheck disable=SC2086 # the stubs' names, one word each
docker compose up -d --wait $stubs >/dev/null 2>&1 &
stack=$!
# With GOCACHE_DIR set (CI), both start from the kept Go build cache. Keeping
# it replaces the directory, so that waits until neither build is reading it.
basebuild=
if [ "$base" != - ]; then
	GOCACHE_KEEP=no sh scripts/build-api-image.sh golang-vibe-starter-api:base "$basesrc" &
	basebuild=$!
fi
GOCACHE_KEEP=no sh scripts/build-api-image.sh golang-vibe-starter-api:head .
if [ -n "$basebuild" ]; then wait "$basebuild"; fi
if [ -n "${GOCACHE_DIR:-}" ]; then
	sh scripts/build-api-image.sh golang-vibe-starter-api:head .
fi
wait "$stack"
cleanup

# serve <side>: (re)start the stack with that side's API image.
serve() {
	API_IMAGE=golang-vibe-starter-api:$1 docker compose up -d --no-build --wait >/dev/null 2>&1
}

# k6 writes summaries into bin/perf (compose mounts it), so they're moved
# into $out by name afterwards.
headfailed=0
measure() {
	side=$1 name=$2
	serve "$side"
	status=0
	sh perf/run.sh "$profile" "ab-$name.json" >"$out/$name.txt" 2>&1 || status=$?
	# Measuring the wrong version would compare a version with itself and
	# report no change, so check the API was this side's throughout.
	image=$(docker inspect --format '{{.Config.Image}}' "$(docker compose ps -q api)")
	if [ "$image" != "golang-vibe-starter-api:$side" ]; then
		echo "The API ran $image during $name, not golang-vibe-starter-api:$side; the comparison is invalid." >&2
		exit 2
	fi
	# The k6 summary, and what perf/resources.sh recorded alongside it.
	for f in json stats.tsv metrics-start.prom metrics-end.prom; do
		if [ -f "bin/perf/ab-$name.$f" ]; then mv "bin/perf/ab-$name.$f" "$out/$name.$f"; fi
	done
	echo "  $name: k6 exit $status"
	if [ "$side" = head ] && [ "$status" -ne 0 ]; then headfailed=1; fi
}

echo "Warming up the stack..."
serve head
sh perf/run.sh "$profile" ab-warmup.json >/dev/null 2>&1 || true
rm -f bin/perf/ab-warmup.*

i=1
while [ "$i" -le "$rounds" ]; do
	echo "round $i of $rounds"
	if [ "$base" = - ]; then
		measure head "head-$i"
	elif [ $((i % 2)) -eq 1 ]; then
		measure base "base-$i"
		measure head "head-$i"
	else
		measure head "head-$i"
		measure base "base-$i"
	fi
	i=$((i + 1))
done

if [ "$headfailed" -ne 0 ]; then
	echo "The working tree crossed the $profile profile's thresholds; k6's output is in $out/head-*.txt."
	exit 1
fi
