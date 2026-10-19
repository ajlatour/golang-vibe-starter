# Releasing

A release is a version tag on `main`.
<!-- feature:release -->
Pushing the tag publishes the API's image to the GitHub Container Registry and a GitHub release,
both made by the Release workflow (`.github/workflows/release.yml`) from the tagged commit.
<!-- end feature:release -->

Versions follow [Semantic Versioning](https://semver.org/): while the version is 0.x, a minor
release (0.2.0) may break callers; from 1.0.0 only a major one may.

## Cutting a release

Two steps, so the changelog edit is reviewed like any change:

```sh
make release-prepare VERSION=v0.1.0   # on an up-to-date main, with no local changes
git push -u origin release-v0.1.0     # then open a pull request and merge it
make release-tag VERSION=v0.1.0       # on main, once it's merged
```

`release-prepare` gathers what's unreleased, the change files in `changes/` (one per pull request
that changed something worth telling; see [its README](../changes/README.md)), into the
changelog's `## [0.1.0] - <today>` section, each heading once in Keep a Changelog's order, and
removes them. It sets the OpenAPI spec's `info.version` to `0.1.0` (`scripts/spec-version.sh`),
and commits it all on the branch `release-v0.1.0`. It refuses when nothing is unreleased, or a
version already released. `release-tag` checks `main` has that section, tags it,
and pushes the tag.

## What keeps a release safe to deploy

Besides the tests every change runs, some exist for releases above all, and run on every pull
request so a problem shows before it's tagged:

- **Known configurations still start.** `cmd/api/testdata/configs` holds configurations
  deployments run with, and `TestKnownConfigsStart` starts this version with each, and with every
  setting at the default `.env.example` documents. A release that would refuse one, or that no
  longer reads one of its settings (silently ignoring it), fails the build. Add your own
  deployments' configurations there, secrets replaced.
- **Breaking API changes are called out, and versioned.** `cmd/apidiff` compares
  `docs/openapi.json` with the last release's and sorts what changed for clients: breaking (an
  operation, success status, response field or documented header removed; a new required
  parameter or field; a request limit tightened; a type changed) or additive (a new operation,
  optional parameter or field, status, or error code). Descriptions don't count. On a pull
  request (`make api-compat`, and CI's docs job, which shows the report in its summary), a
  breaking change needs a `**Breaking**` entry in a change file in `changes/`, so it's a
  decision, written down for the release notes. For a release, `make release-prepare` refuses a
  version bump too small for the changes: an additive change needs at
  least a minor release, and a breaking one a minor release while the version is 0.x, a major
  one from 1.0.0. `sh scripts/api-compat.sh release vX.Y.Z` runs that check alone.

<!-- feature:release -->
- **The image that ships is run before it's published.** `scripts/smoke-image.sh` runs an image
  for one platform against WireMock and flagd and checks: it is that platform's image, it says
  it's the version being released, it runs as a non-root user, the probes answer, a request
  without a key is refused, the items list, an item has an `ETag`, a create works, an unknown
  path gets a JSON 404, and SIGTERM stops it cleanly (exit 0, `server stopped`). The Release
  workflow runs it on the image it's about to publish (above); `make smoke` builds this machine's
  image and runs it (`PLATFORM=linux/amd64` for the other one, emulated). Its dependencies run in
  a compose project of their own, `vibe-smoke`, with no ports published, so it runs beside a
  local stack.

## What the tag publishes

The workflow checks the tag is on a commit that's on `main`, that the changelog has the
version's section, and that `docs/openapi.json` is that version, runs `make check` once more on
the tagged commit, and builds and checks the documentation (`make docs-release`). Then it builds
the image, for both platforms, and pushes it by digest alone, with no tag. It runs that exact
image on each platform, by the digest of that platform's own manifest within it, arm64 under
emulation, against WireMock and flagd
(`scripts/smoke-image.sh`, below), and only if both pass points the version's tags at that digest:
nothing is rebuilt between the test and the release, and the workflow checks the tags name the
tested digest, with its SBOM and provenance. A failed smoke test leaves an untagged image in the
registry, and no release. Then it publishes:

| What | Where |
| ---- | ----- |
| The API's image, for linux/amd64 and linux/arm64 | `ghcr.io/ajlatour/golang-vibe-starter`, tagged `v0.1.0`, `0.1` and `latest` |
| An SBOM (SPDX) and build provenance (SLSA v1) for each platform | Attached to the image in the registry, made by BuildKit |
| A GitHub release | Its notes are the version's changelog section, headed by the image's name |
| The documentation, `docs-v0.1.0.tar.gz` | Attached to the release: the docs site as of the tag (open `docs/index.html`), with a Performance history page charting `main`'s performance and CI time up to the release |
| The performance records, `performance-records-v0.1.0.tar.gz` | Attached to the release: the `perf-data` records that page charts, as JSON, for analysis of your own |

<!-- feature:perf -->
So each release carries the history up to it, and comparing two releases' Performance history
pages shows what changed between them. The history comes from the `perf-data` branch as it is when
the release is made ([Performance](performance.md#trends)).

<!-- end feature:perf -->

The binary inside reports the version: `version` on every log line is `v0.1.0`. The image's
labels name the source repository, the commit and the version. The package is private, like the
repository: pulling it needs `docker login ghcr.io` with a token that can read packages.

To see an image's SBOM or provenance:

```sh
docker buildx imagetools inspect ghcr.io/ajlatour/golang-vibe-starter:v0.1.0 --format '{{ json .SBOM }}'
docker buildx imagetools inspect ghcr.io/ajlatour/golang-vibe-starter:v0.1.0 --format '{{ json .Provenance }}'
```

## A dry run

Run the Release workflow by hand (Actions, Release, Run workflow) to rehearse on any branch: it
makes the same checks and the same multi-platform build, with the SBOM and provenance, smoke-tests
each platform's image, and publishes nothing. `sh scripts/changelog.sh notes` prints what the next release's notes would be.

## Not yet

Images aren't signed. Signing with cosign and keyless OIDC would need a third-party tool, and
GitHub's own artifact attestations aren't available to private repositories on this plan. The
provenance attached to the image records how it was built, but not who vouches for it.

<!-- end feature:release -->
