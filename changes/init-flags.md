### Added

- `make init WITHOUT=flags` leaves out feature flags: `internal/featureflag`, `flagd/` and the
  flagd sidecar in every stack, their settings, log lines and integration steps, and the example
  list's `total`. Request validation, if kept, then always acts on the service's answers.

### Changed

- A resource's wiring takes what the API shares (`deps`), so its signature doesn't depend on which
  parts the service has.
- A `feature:!NAME` block is commented out in the template, so it does nothing there; `make init`
  uncomments it in a service without NAME. And it drops the imports only a left-out block used.
