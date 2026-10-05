#!/bin/sh
# Runs the Go benchmarks of two versions of the code, the working tree and a
# base commit, interleaved on this machine. `make bench-compare` and the perf
# workflow call it; see docs/performance.md.
#
#   sh perf/bench-ab.sh <base ref> <rounds> <output dir>
#
# Writes <output dir>/base.txt and head.txt in Go's benchmark format, for
# benchstat or perf/report. Each round runs every benchmark once for each
# version, alternating which goes first, so anything that slows the machine
# for a while (another process, thermal throttling, a noisy neighbour on a
# shared runner) lands on both versions alike instead of skewing one.
set -eu

base=${1:?usage: bench-ab.sh <base ref> <rounds> <output dir>}
rounds=${2:?usage: bench-ab.sh <base ref> <rounds> <output dir>}
out=${3:?usage: bench-ab.sh <base ref> <rounds> <output dir>}
mkdir -p "$out"
out=$(cd "$out" && pwd)
head=$(pwd)
basesrc=$out/base-src
bins=$out/bin

cleanup() { git worktree remove --force "$basesrc" 2>/dev/null || true; rm -rf "$bins"; }
trap cleanup EXIT
cleanup
git worktree add --quiet --detach "$basesrc" "$base"
mkdir -p "$bins/base" "$bins/head"

# The packages with benchmarks, as directories relative to a tree's root
# (cmd/api), each named in bin/ with its slashes as underscores.
benchdirs() {
	(cd "$1" && grep -rl --include='*_test.go' --exclude-dir=bin '^func Benchmark' . | sed -e 's|/[^/]*$||' -e 's|^\./||' | sort -u)
}

# Compile each package's test binary once, so the rounds below time the
# benchmarks rather than the compiler. A package with no benchmarks at the
# base is simply missing there.
for side in base head; do
	if [ "$side" = base ]; then tree=$basesrc; else tree=$head; fi
	for dir in $(benchdirs "$tree"); do
		name=$(echo "$dir" | tr / _)
		(cd "$tree" && go test -c -o "$bins/$side/$name.test" "./$dir")
	done
done

# run <side> <tree>: one round of every benchmark of one version, run in its
# package's directory as go test would.
run() {
	for bin in "$bins/$1"/*.test; do
		[ -e "$bin" ] || continue
		dir=$(basename "$bin" .test | tr _ /)
		(cd "$2/$dir" && "$bin" -test.run '^$' -test.bench . -test.benchmem -test.count 1) >>"$out/$1.txt"
	done
}

: >"$out/base.txt"
: >"$out/head.txt"
i=1
while [ "$i" -le "$rounds" ]; do
	echo "round $i of $rounds"
	if [ $((i % 2)) -eq 1 ]; then
		run base "$basesrc"
		run head "$head"
	else
		run head "$head"
		run base "$basesrc"
	fi
	i=$((i + 1))
done
echo "benchmarks: $out/base.txt and $out/head.txt"
