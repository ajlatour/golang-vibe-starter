# ADR-0024: Main's performance history lives on a data branch

- **Status:** Accepted
- **Date:** 2026-10-05 (9f7c99b)

**Context.** Pull request comparisons (ADR-0013) catch a change worse than its base, not a slow
drift across merges. Dashboards and benchmark services need hosting; GitHub Pages needs a paid
plan for a private repository.

**Decision.** After every merge, measure `main` alone and save one JSON record per commit on the
`perf-data` branch. `make perf-trends` charts the records as a standalone HTML page, drawn with
inline SVG by the report tool.

**Consequences.** The history lives in git, with no service to run (PERF-3). The recording job is
the only one that writes to the repository, only to that branch, and runs only merged code.
Timings carry the runners' noise; allocations, being exact, chart cleanly.
