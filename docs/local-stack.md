# The local stack

`make up` (or `docker compose up -d --build --wait`) starts three containers:

| Service | Port (host) | What it is |
| ------- | ----------- | ---------- |
| `api`   | 8080 | The API, built from `cmd/api/Dockerfile`. |
| `mock`  | 8081 | [WireMock](https://wiremock.org), standing in for the items upstream. Admin API at `/__admin`. |
| `flagd` | 8016 | The feature flag service. See [Feature flags](feature-flags.md). |

Every port is published on this machine's loopback (`127.0.0.1`) only, so on a shared network
nobody else can reach the stack, least of all WireMock's admin API, which has no authentication
and can change everything the API is served. To reach the stack from another device on purpose,
change the address in `compose.yaml` for that run, and change it back.

The API starts only after WireMock passes its healthcheck. The API image is distroless and static
(about 16 MB). It has no shell or curl, so it checks its own health with `/api healthcheck`.

To run the API outside Docker, with `go run`, against the stack's other containers:

```sh
make run
```

That starts them and runs `go run ./cmd/api` with their URLs set (`UPSTREAM_URL` and the
rest) and text logs, stamping the current commit as the version.

`compose.integration.yaml` is an override for the integration tests; see
[Development](development.md#integration-tests).

## WireMock stubs

**Stubs** live in `wiremock/mappings/` and are mounted read-only. They serve the `/items` contract:
list, get, create, update, delete, 404 for unknown IDs, 422 for invalid names, 400 for malformed
bodies, and 405 for other methods.

- **No state is kept.** WireMock always lists `widget`, `gadget` and `gizmo`. Create returns
  `new-item`, echoing the `name` and `description` you sent, and that ID can then be updated or
  deleted. Nothing is actually stored, so a GET of `new-item` returns 404, and deletes don't
  remove anything.
- **Reads are fully static.** Create and update use response templating so the reply echoes your
  input; those requests aren't the load-test hot path.

To reload stubs after editing the files: `curl -X POST localhost:8081/__admin/mappings/reset`.

## Fault injection

Fault stubs live in `wiremock/faults/`. They aren't mounted, so none are active by default. Each
file is a single stub with a fixed `id` and the highest priority, so it overrides the matching
happy-path stub while it's loaded:

| File                    | Effect on the upstream                          | What the API does                    |
| ----------------------- | ----------------------------------------------- | ------------------------------------ |
| `slow-list.json`        | `GET /items` takes 2s                           | 200 after ~2s                        |
| `timeout-list.json`     | `GET /items` takes 8s                           | 504 after 5s (`UPSTREAM_TIMEOUT`)    |
| `unavailable.json`      | Every `/items` request returns 503              | 502, logs the upstream 503           |
| `connection-reset.json` | Every `/items` connection is reset              | 502, logs the connection reset       |
| `malformed-list.json`   | `GET /items` returns truncated JSON             | 502, logs the decode error           |

```sh
make fault FAULT=unavailable     # switch one on (with no FAULT, lists the names)
make fault-reset                 # switch them all off
```

What those do, with curl:

```sh
# turn a fault on
curl -X POST localhost:8081/__admin/mappings -H 'Content-Type: application/json' \
  --data @wiremock/faults/unavailable.json
# turn it off again (the ID is in the file)
curl -X DELETE localhost:8081/__admin/mappings/f0000000-0000-4000-8000-000000000003
# or drop every runtime change and go back to the mounted stubs
curl -X POST localhost:8081/__admin/mappings/reset
```

The integration tests switch these same stubs on, one scenario at a time.

<!-- feature:perf -->
## Load-test tuning

[Performance](performance.md) covers running the load tests; this section covers WireMock's part.

WireMock's defaults suit unit-test-style verification, not long or high-volume runs. `compose.yaml`
sets:

- `--no-request-journal`: by default WireMock keeps every request in memory for later
  verification, and the heap grows without limit over a long run. The trade-off is that
  `/__admin/requests` and the other verification endpoints return an error. Remove the flag
  temporarily if you need to inspect requests.
- `--disable-request-logging`: no log line per request.
- `--async-response-enabled=true` and `--async-response-threads=32`: delayed responses (the slow
  and timeout faults) wait on a separate pool instead of holding Jetty request threads.
- `--container-threads=200`: a larger Jetty request thread pool, for high concurrency.
- `--disable-gzip`: no compression work on either side of a local network hop.
- `JAVA_OPTS=-Xms512m -Xmx512m`: a fixed heap, so it never pauses to resize mid-run.

The image is pinned to `wiremock/wiremock:3.13.2`, a multi-arch image that runs natively on arm64
and amd64. The `-alpine` tag is amd64-only and would run under emulation on Apple Silicon, which
distorts timings.

**Tail latency includes the mock.** WireMock runs on the JVM. The first requests run before the JIT
compiler has warmed up, and garbage-collection pauses arrive in bursts. Both show up in the API's
p99 and p99.9 as upstream latency. When load testing:

- Run a warm-up phase and leave it out of the results.
- Before blaming the API for a tail-latency spike, check whether WireMock was the cause, e.g. by
  enabling JVM GC logging through `JAVA_OPTS`.
- WireMock competes with the API and the load generator for CPU. Consider capping it with `cpus:`
  in compose, and keep the load generator's CPU usage in view.

<!-- end feature:perf -->
