### Added

- `make init WITHOUT=perf` leaves out performance testing: the k6 load tests and their pull request
  report and allocation gate, main's performance history, the nightly soak, CI-time records and the
  Grafana monitoring stack. `WITHOUT=release` leaves out publishing: the Release workflow, the
  image's smoke test and the SBOM generator, keeping versioning (`make release-prepare`,
  `release-tag`). CI checks each, alone and together, with the tests, shellcheck, actionlint and
  the docs site.

### Changed

- `make resource` skips the load test's traffic mix in a service without one.
- The Makefile declares each section's targets `.PHONY` beside them, and `make lint-files` runs
  shellcheck and actionlint alone.
