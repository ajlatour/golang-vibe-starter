# Performance records

records/ holds one JSON file per commit on main, written by .github/workflows/perf-trends.yml;
ci/ one per successful CI run on main, written by .github/workflows/ci-times.yml (the first
22, up to 6bd7e0a, were backfilled from the GitHub API when it was added).
Run `make perf-trends` on main to chart them; each release attaches them, charted, to its docs.
