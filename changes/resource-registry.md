### Changed

- `cmd/api` wires each resource from a file of its own (`items.go`), listed in `resources.go`, and
  the spec tests take each resource's stores and scenarios from its test file (`items_test.go`),
  listed in `resources_test.go`: a second resource is new files and a line in each list, not
  edits to the shared code and tests. No change in behaviour.
