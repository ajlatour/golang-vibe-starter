#!/bin/sh
# Runs a k6 profile against two versions of the API, the working tree and a
# base commit, in turn on the same stack. `make load-compare` and the perf
# workflow call it; see docs/performance.md.
#
#   sh perf/load-ab.sh <base ref> <rounds> <output dir> [profile]
#
# Builds an API image for each version, then for each round runs the profile
# (default compare) against each, alternating which goes first, with the same
# WireMock and flagd throughout. A first run against the working tree warms
# up the stack (WireMock's JIT above all) and is thrown away. Summaries are
# written as <output dir>/base-<n>.json and head-<n>.json.
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
git worktree add --quiet --detach "$basesrc" "$base"

echo "Starting WireMock and flagd, and building the API at $base and in the working tree..."
# All three at once: the two builds share BuildKit's base images and run
# their Go builds side by side, while the other containers start.
docker compose up -d --wait mock flagd >/dev/null 2>&1 &
stack=$!
docker build --quiet -f "$basesrc/cmd/api/Dockerfile" -t golang-vibe-starter-api:base "$basesrc" >/dev/null &
basebuild=$!
docker build --quiet -f cmd/api/Dockerfile -t golang-vibe-starter-api:head . >/dev/null
wait "$basebuild"
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
	mv "bin/perf/ab-$name.json" "$out/$name.json"
	echo "  $name: k6 exit $status"
	if [ "$side" = head ] && [ "$status" -ne 0 ]; then headfailed=1; fi
}

echo "Warming up the stack..."
serve head
sh perf/run.sh "$profile" ab-warmup.json >/dev/null 2>&1 || true
rm -f bin/perf/ab-warmup.json

i=1
while [ "$i" -le "$rounds" ]; do
	echo "round $i of $rounds"
	if [ $((i % 2)) -eq 1 ]; then
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
