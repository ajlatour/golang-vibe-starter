# Known configurations

Each `.env` file here is a configuration a deployment runs with, and
`TestKnownConfigsStart` (`cmd/api/configs_test.go`) checks this version of the API
starts with every one, and reads every variable it sets. A change that would
refuse one, or silently ignore one of its settings, fails the build: decide on
purpose, then update the file and say so in the changelog.

Add your own deployments' configurations here, with secrets replaced: API key
hashes by any valid one (`make api-key`), URLs by any URL. `ADDR`, `PORT` and
`PPROF_ADDR` are replaced by free local addresses when the test runs, and
`API_KEYS_FILE` is read relative to this directory.

- `minimal.env`: only what's required.
- `production.env`: a typical production deployment: every dependency, its own
  timeouts, a key file, a shutdown delay for the load balancer.
- `v0.5.env`: a production configuration written for v0.5.0, before
  `REQUEST_TIMEOUT` existed. It must keep starting, or the release that breaks
  it must say so.
