### Added

- `make init WITHOUT=validation` leaves out request validation: `internal/validator` and its
  wiring (now `cmd/api/validation.go`), its settings, log lines, metrics, readiness check, error
  codes and flag, its docs and WireMock's stub of the service.

### Changed

- A GET's 403 in the spec is its own response, `Refused`: the request-validation service's
  refusal, the only one a read can get. A write's stays `Forbidden`. Clients see no change.
- The scenario tests' "overloaded" environment holds the service's one slot with a create whose
  body never ends, rather than with a request-validation check that never answers.
