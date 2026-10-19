### Added

- `make init ... WITHOUT=tracing` (or `WITHOUT=all`) starts a service without the template's
  optional parts; so far, tracing, which takes OpenTelemetry, its settings, log lines, docs and
  Jaeger with it, leaving the service without a third-party dependency. `-list-features` lists
  them, as JSON, for tools. Each is marked in the code (`// feature:tracing` … `// end
  feature:tracing`) or listed in `cmd/initservice/features.go`, and CI initializes a copy with
  each left out and runs its tests, and builds the docs of one with all of them out.
- `make init` also leaves out its own tool and targets, and starts the service's `ROADMAP.md`
  afresh.
- A decision record can be `Deprecated: <why>`, for one that no longer applies: `make init` marks
  so the records of the features it leaves out.
