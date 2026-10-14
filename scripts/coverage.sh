#!/bin/sh
# Summarises a coverage profile by package, and fails if the total is under a
# floor. `make cover` and CI's test job run it; see docs/development.md.
#
#   sh scripts/coverage.sh <profile> <minimum percent>
#
# The profile comes from go test -coverpkg, so a package's statements count
# as covered when any package's tests run them: the handlers' tests cover
# httpx's middleware too. A block several test binaries report is counted
# once. The table also goes to the job summary in CI.
set -eu

profile=${1:?usage: coverage.sh <profile> <minimum percent>}
min=${2:?usage: coverage.sh <profile> <minimum percent>}
module=$(go list -m)

table=$(awk -v module="$module/" '
	NR == 1 { next } # mode: atomic
	{
		# file.go:12.3,15.4 statements count
		split($1, loc, ":")
		key = $1
		stmts[key] = $2
		if ($3 > 0) hit[key] = 1
	}
	END {
		for (key in stmts) {
			split(key, loc, ":")
			file = loc[1]
			sub(module, "", file)
			pkg = file
			sub(/\/[^\/]+$/, "", pkg)
			total[pkg] += stmts[key]
			if (key in hit) covered[pkg] += stmts[key]
			all += stmts[key]
			if (key in hit) allc += stmts[key]
		}
		for (pkg in total) printf "%s\t%.1f\t%d\n", pkg, 100 * covered[pkg] / total[pkg], total[pkg]
		printf "TOTAL\t%.1f\t%d\n", 100 * allc / all, all
	}
' "$profile" | sort)

total=$(echo "$table" | awk -F'\t' '$1 == "TOTAL" { print $2 }')
{
	echo "## Coverage: $total% (the floor is $min%)"
	echo
	echo "| Package | Covered | Statements |"
	echo "| ------- | ------: | ---------: |"
	echo "$table" | awk -F'\t' '$1 != "TOTAL" { printf "| `%s` | %s%% | %s |\n", $1, $2, $3 }'
} | tee -a "${GITHUB_STEP_SUMMARY:-/dev/null}"

if awk -v t="$total" -v m="$min" 'BEGIN { exit !(t + 0 < m + 0) }'; then
	echo "coverage: $total% is under the $min% floor: add tests for what this change left uncovered" >&2
	exit 1
fi
