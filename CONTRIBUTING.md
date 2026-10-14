# Contributing

Thanks for helping. This is the short version; the [standards](docs/standards.md) are the rules,
and [Development](docs/development.md) explains how the code is put together and tested.

## Set up

You need Go (the version in `go.mod`, which `GOTOOLCHAIN=auto` fetches for you), Docker with
Compose, and `make`. Nothing else: every other tool runs through `go run` or in Docker.

```sh
make check          # format, vet, and the unit tests with the race detector: seconds, no Docker
make lint           # static analysis, as CI runs it
make up             # the API with its mock upstream and flagd, on http://localhost:8080
make integration    # the end-to-end scenarios against that stack
```

`make help` lists everything.

## Make a change

1. Branch from `main`. Keep the change to one concern.
2. Change the code, its tests, and what describes it: the spec, the docs, the log contract. A
   test fails, naming what to update, if you miss one ([what goes with what](docs/development.md#making-a-change)).
3. For anything users of a release should know, add a file to [`changes/`](changes/README.md).
   For an architectural choice, add a [decision record](docs/decisions.md).
4. Run `make check` and `make lint`, and `make docs-check` if you touched the docs.
5. Open a pull request. The template asks what changed, why, and how you tested it.

CI runs the same `make` targets. A pull request merges once every check is green; a check is
never disabled to get there.

## Report a security problem

Privately, please, as [`SECURITY.md`](SECURITY.md) explains, not in an issue.
