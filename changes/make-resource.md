### Added

- `make resource NAME=order` (and `PLURAL=…` when it isn't the name with an s) adds a resource to
  the API, copied from `/items` under the new name: its package, with the model, store interface,
  handlers, stores and contract tests; its wiring and spec tests in `cmd/api`; its paths and
  schemas in the spec; its WireMock stubs, flag, curl examples, load-test traffic and integration
  feature. It builds and passes every check as generated, and says what to change to make it the
  resource you mean. `make resource-check`, run in CI, does it on a copy of the template and runs
  the tests.
