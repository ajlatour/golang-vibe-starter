### Added

- `make init NAME=orders MODULE=github.com/acme/orders` makes a copy of the template a service of
  its own: it renames the module, the GitHub repository the workflows check for, the service's
  name, images and compose project, and the code owners; starts the changelog at 0.0.0, saying
  which template version it came from; empties `changes/`; and says what's left to do by hand.
  `make init-check`, run in CI, does it on a copy of the template and vets and tests the result.

### Fixed

- The store contract test's check that pages stay right while the list changes no longer fails
  now and then when two items get the same creation time, which a clock with microsecond
  resolution allows.
