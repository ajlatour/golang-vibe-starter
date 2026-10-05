# Feature flags

[flagd](https://flagd.dev), an OpenFeature-compliant flag service, runs next to the API as a
sidecar. The API evaluates flags over [OFREP](https://openfeature.dev/specification/appendix-c)
(the OpenFeature Remote Evaluation Protocol), which is plain HTTP/JSON, so the project stays
standard-library only. There is no OpenFeature SDK or gRPC dependency.

## The flags

Flags are defined in `flagd/flags.json`, which flagd watches and reloads on change, with no
restart.

| Flag               | Type    | Default | Effect                                              |
| ------------------ | ------- | ------- | --------------------------------------------------- |
| `items-list-total` | boolean | off     | `GET /items` also returns `"total": <item count>`.  |

To try one locally, with the stack running:

```sh
curl -s -X POST localhost:8016/ofrep/v1/evaluate/flags/items-list-total -d '{"context":{}}'
# edit flagd/flags.json: set "defaultVariant" to "on", then
curl localhost:8080/items        # now includes "total"
```

Edit the file in place. An editor that saves by writing a new file and renaming it over the old
one can occasionally make flagd lose its watch on the file and stop reloading until it is
restarted; `integration/flags_test.go` explains why.

## When flagd can't answer

Flags never fail a request. If flagd is unreachable, slow (`FLAGS_TIMEOUT`), or doesn't know the
flag, the API logs a warning and falls back to the flag's `defaultVariant` from
`flagd/flags.json`. That is the same file flagd serves, compiled into the API binary
(`flagd/embed.go`), so there is one place to define a flag and the fallback can't drift from it.

- Set `FLAGS_FALLBACK_FILE` to read a different flagd-format file at startup instead (for example
  one mounted from a ConfigMap).
- With `FLAGS_URL` unset the API skips flagd and uses those defaults alone.
- The API keeps each value flagd serves for `FLAGS_CACHE_TTL` (1s), so a change made in flagd
  reaches it within a second, and flagd is asked about each flag about once a second however busy
  the API is. While flagd isn't answering, the API asks it once per `FLAGS_CACHE_TTL` too, and
  every evaluation in between takes its default at once instead of waiting out `FLAGS_TIMEOUT`.
- Only boolean flags are supported; a flag whose default variant isn't a boolean stops the
  service at startup.

How flag problems are logged, without flooding the logs during an outage, is in
[Logging](logging.md#how-to-read-a-request).

## Adding a flag

1. Define it in `flagd/flags.json`.
2. Declare and read it in code:
   ```go
   var FlagFoo = featureflag.Declare("foo") // package level
   ...
   if f.Bool(ctx, FlagFoo) { ... }   // f is the package's Flags
   ```

`featureflag.Declare` registers the key, and the service refuses to start if any declared flag is
missing from the definitions, so a typo or forgotten step fails fast, not at request time.

Evaluation currently sends an empty context, so targeting rules in flagd (per user, per tenant)
have nothing to match on yet. Pass request attributes in `featureflag.OFREP` when you need them.
