### Added

- `make init ... RESOURCE=order` (and `RESOURCE_PLURAL=` for a plural that isn't the name with an
  s) names the example resource for the service's first: `/items` becomes `/orders` everywhere,
  in its package, files, types, routes, spec, stubs, tests and docs. The lists' JSON key, `items`,
  stays: every resource's list has it.
- `make resource` copies from any resource (`-from`), and never renames the module's path, so a
  service named for its resource (`orders`, with `/orders`) can add another.

### Changed

- The resource renamer is `internal/rename`, shared by `make init` and `make resource`. It keeps
  `items` only where it's the lists' JSON key, so a quoted plural elsewhere is renamed.
- The in-memory store's map is `byID`.
