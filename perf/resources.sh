#!/bin/sh
# Records what a load run costs: each container's CPU and memory, sampled by
# docker stats about once a second, and the API's own runtime metrics
# (internal/profiling) from just before and just after the run. perf/run.sh
# calls it around every k6 run; see docs/performance.md#watching-resources.
#
#   sh perf/resources.sh start <stem>
#   sh perf/resources.sh stop <stem>
#
# start begins sampling into <stem>.stats.tsv and saves the API's metrics as
# <stem>.metrics-start.prom; stop ends the sampling, saves
# <stem>.metrics-end.prom and prints a summary. Either set of numbers is
# skipped, not an error, if it isn't available: docker stats needs the stack
# running, and the metrics need the API serving them (PPROF_ADDR, which the
# compose stack sets) at PPROF_URL (default http://localhost:6061).
set -eu

cmd=${1:?usage: resources.sh start|stop <stem>}
stem=${2:?usage: resources.sh start|stop <stem>}
pprof=${PPROF_URL:-http://localhost:6061}

scrape() {
	curl -fsS -m 5 -o "$stem.$1.prom" "$pprof/metrics" 2>/dev/null || rm -f "$stem.$1.prom"
}

# value <file> <metric>: a metric's value in a scraped file.
value() {
	awk -v m="$2" '$1 == m { print $2; exit }' "$1"
}

case $cmd in
start)
	scrape metrics-start
	# One process writing straight to the file, so stop can end it with a
	# single kill. Without a terminal, docker stats still streams a block of
	# lines per refresh.
	docker stats --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' >"$stem.stats.tsv" 2>/dev/null &
	echo $! >"$stem.stats.pid"
	;;
stop)
	if [ -f "$stem.stats.pid" ]; then
		kill "$(cat "$stem.stats.pid")" 2>/dev/null || true
		rm -f "$stem.stats.pid"
	fi
	scrape metrics-end

	echo
	echo "Resources during the run (docker stats, about once a second):"
	# Only this project's containers count. Their names lose the compose
	# project prefix and replica suffix, and k6's one-off run containers all
	# count as k6. Memory is converted to MiB.
	awk -F'\t' '
		{ gsub(/\033\[[0-9;]*[A-Za-z]/, "") }
		NF < 3 { next }
		# docker stats shows every container on the machine.
		$1 !~ /^golang-vibe-starter-/ { next }
		{
			name = $1
			sub(/^golang-vibe-starter-/, "", name)
			sub(/-run-[0-9a-f]+$/, "", name)
			sub(/-[0-9]+$/, "", name)
			cpu = $2; sub(/%/, "", cpu)
			split($3, mem, " / ")
			m = mem[1]; mib = m + 0
			if (m ~ /GiB/) mib *= 1024
			else if (m ~ /KiB/) mib /= 1024
			else if (m ~ /[0-9]B$/) mib /= 1048576
			if (!(name in n)) order[++count] = name
			n[name]++; sum[name] += cpu
			if (cpu > peak[name]) peak[name] = cpu
			if (mib > memmax[name]) memmax[name] = mib
		}
		END {
			if (count == 0) { print "  (no samples: is the stack running?)"; exit }
			printf "  %-10s %9s %9s %12s\n", "container", "CPU avg", "CPU peak", "memory peak"
			for (i = 1; i <= count; i++) {
				c = order[i]
				printf "  %-10s %8.1f%% %8.1f%% %8.1f MiB\n", c, sum[c] / n[c], peak[c], memmax[c]
			}
			print "  (100% is one CPU core.)"
		}
	' "$stem.stats.tsv"

	if [ -f "$stem.metrics-start.prom" ] && [ -f "$stem.metrics-end.prom" ]; then
		s=$stem.metrics-start.prom e=$stem.metrics-end.prom
		awk -v cpu0="$(value "$s" process_cpu_seconds_total)" -v cpu1="$(value "$e" process_cpu_seconds_total)" \
			-v a0="$(value "$s" go_gc_heap_allocs_bytes_total)" -v a1="$(value "$e" go_gc_heap_allocs_bytes_total)" \
			-v g0="$(value "$s" go_gc_cycles_total_gc_cycles_total)" -v g1="$(value "$e" go_gc_cycles_total_gc_cycles_total)" \
			-v gr="$(value "$e" go_sched_goroutines_goroutines)" -v heap="$(value "$e" go_memory_classes_heap_objects_bytes)" \
			'BEGIN {
				print "The API process (its /metrics, before and after):"
				printf "  CPU time %.2fs, allocated %.1f MiB in %d GC cycles; at the end %.1f MiB of heap and %d goroutines\n",
					cpu1 - cpu0, (a1 - a0) / 1048576, g1 - g0, heap / 1048576, gr
			}'
	fi
	;;
*)
	echo "usage: resources.sh start|stop <stem>" >&2
	exit 2
	;;
esac
