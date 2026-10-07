# ADR-0013: Pull requests are compared with their base on one runner

- **Status:** Accepted
- **Date:** 2026-10-04 (2236ebf, d94897d)

**Context.** Absolute timings from shared CI runners vary by 10% or more between runners, so a
number from one run means little. A gate on noisy numbers either blocks good changes or is
ignored. Of the load tools considered (a hand-written Go generator, Vegeta as a library, k6), k6
was chosen, run from its image (ADR-0011).

**Decision.** Run each pull request's benchmarks and a short k6 profile against both the pull
request and its base, interleaved on the same runner, and report the comparison in one comment
edited on every push. Fail only on what is exact or plainly broken: more allocations per
operation (unless the pull request has the `perf-ok` label) or crossed load thresholds. Report
timing, CPU and memory changes as warnings. Write the comment from a job that runs no pull
request code (ADR-0020).

**Consequences.** Regressions in allocations can't land unnoticed (PERF-2). Timing regressions
are flagged but need confirming locally (`make perf-compare`). The comparison adds about four
minutes to a pull request, in parallel with CI.
