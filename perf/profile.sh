#!/bin/sh
# Runs a k6 profile and captures the API's runtime profiles (pprof) during and
# after it. `make perf-profile PROFILE=<name>` starts the stack and calls this;
# see docs/performance.md.
#
#   sh perf/profile.sh <profile> <pprof base URL> <output directory>
#
# After a 40s warm-up it takes heap and goroutine snapshots, then a 30s CPU
# profile and an allocation profile while the load runs. When the run ends it
# takes heap and goroutine snapshots again: anything still held once the load
# has stopped, beyond the first snapshot, is a candidate leak.
set -eu

profile=${1:?usage: profile.sh <profile> <pprof URL> <output dir>}
pprof=${2:?usage: profile.sh <profile> <pprof URL> <output dir>}
out=${3:?usage: profile.sh <profile> <pprof URL> <output dir>}
mkdir -p "$out"

fetch() {
	curl -fsS -o "$out/$1.pprof" "$pprof/debug/pprof/$2" || {
		echo "fetching $pprof/debug/pprof/$2 failed. Is the API serving profiles (PPROF_ADDR)?" >&2
		return 1
	}
}

echo "Running the $profile profile; k6's output goes to $out/k6.txt."
sh perf/run.sh "$profile" >"$out/k6.txt" 2>&1 &
k6=$!

status=0
sleep 40
echo "Profiling: heap and goroutines, then 30s of CPU, then allocations..."
fetch heap-start heap && fetch goroutine-start goroutine &&
	fetch cpu 'profile?seconds=30' && fetch allocs allocs || status=$?

echo "Waiting for the load run to finish..."
k6status=0
wait "$k6" || k6status=$?
if [ "$status" -eq 0 ]; then
	fetch heap-end 'heap?gc=1' && fetch goroutine-end goroutine || status=$?
fi
tail -n 40 "$out/k6.txt"

[ "$status" -eq 0 ] || exit "$status"
cat <<EOF

Profiles are in $out. Explore one in the browser:
  go tool pprof -http=: $out/cpu.pprof
  go tool pprof -http=: -sample_index=alloc_space $out/allocs.pprof
What the run left behind, against the snapshot taken under load:
  go tool pprof -http=: -diff_base=$out/heap-start.pprof $out/heap-end.pprof
  go tool pprof -http=: -diff_base=$out/goroutine-start.pprof $out/goroutine-end.pprof
EOF
exit "$k6status"
