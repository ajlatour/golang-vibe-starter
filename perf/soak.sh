#!/bin/sh
# Runs the soak profile against the running stack, sampling the API's runtime
# metrics (internal/profiling) once a minute, for `report soak` to judge
# whether its heap or goroutines grew. `make perf-soak` and the nightly Soak
# workflow call it; see docs/performance.md#soak.
#
#   sh perf/soak.sh <output dir> [profile]
#
# Leaves in <output dir>: soak.samples.tsv (the samples), and what
# perf/run.sh leaves for any profile: soak.json (k6's summary), soak.txt
# (k6's output), and .stats.tsv and .metrics-{start,end}.prom. The profile is
# soak unless given, which only a quick try of the script needs. The metrics
# come from PPROF_URL (default http://localhost:6061), as for
# perf/resources.sh; a minute without them is a gap in the samples, which
# `report soak` refuses to judge if there are too many. SOAK_INTERVAL sets
# the seconds between samples (default 60).
#
# The exit status is k6's: 0 when the profile's thresholds held.
set -eu

out=${1:?usage: soak.sh <output dir> [profile]}
profile=${2:-soak}
interval=${SOAK_INTERVAL:-60}
pprof=${PPROF_URL:-http://localhost:6061}
mkdir -p "$out"
samples=$out/soak.samples.tsv

# sample: one line of samples from the API's metrics, the seconds since
# start first. Columns are named in the header; `report soak` finds them by
# name.
start=$(date +%s)
sample() {
	curl -fsS -m 5 "$pprof/metrics" 2>/dev/null | awk -v t="$(($(date +%s) - start))" '
		$1 == "go_gc_heap_live_bytes" { heap = $2 }
		$1 == "go_sched_goroutines_goroutines" { gr = $2 }
		$1 == "go_memory_classes_total_bytes" { mapped = $2 }
		END { if (heap != "" && gr != "" && mapped != "") printf "%s\t%s\t%s\t%s\n", t, heap, gr, mapped }
	'
}

printf 'seconds\theap_live_bytes\tgoroutines\tmapped_bytes\n' >"$samples"
(
	while :; do
		sample >>"$samples" || true
		sleep "$interval"
	done
) &
sampler=$!
trap 'kill "$sampler" 2>/dev/null || true' EXIT

echo "Running the $profile profile, sampling the API's heap and goroutines every ${interval}s..."
status=0
sh perf/run.sh "$profile" soak.json >"$out/soak.txt" 2>&1 || status=$?
# Only samples under load: once k6 stops, the API idles, a floor of its own.
kill "$sampler" 2>/dev/null || true

for f in json stats.tsv metrics-start.prom metrics-end.prom; do
	if [ -f "bin/perf/soak.$f" ] && ! [ "bin/perf/soak.$f" -ef "$out/soak.$f" ]; then mv "bin/perf/soak.$f" "$out/soak.$f"; fi
done
tail -n 40 "$out/soak.txt"
echo "samples: $samples ($(($(wc -l <"$samples") - 1)) of them)"
exit "$status"
