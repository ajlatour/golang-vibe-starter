### Added

- `STORE=memory` keeps items in the process's memory, so the API runs with nothing else: no
  upstream, no containers. `make dev` runs it that way, with `go run` and the local development
  keys, so a fresh clone serves requests in seconds. It's for development and tests: items are lost
  when the service stops, it holds at most 10,000, and startup warns that it's in use. `STORE`
  defaults to `upstream`, as before.
