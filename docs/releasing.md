# Releasing

A release is a version tag on `main`. Pushing the tag publishes the API's image to the GitHub
Container Registry and a GitHub release, both made by the Release workflow
(`.github/workflows/release.yml`) from the tagged commit.

Versions follow [Semantic Versioning](https://semver.org/): while the version is 0.x, a minor
release (0.2.0) may break callers; from 1.0.0 only a major one may.

## Cutting a release

Two steps, so the changelog edit is reviewed like any change:

```sh
make release-prepare VERSION=v0.1.0   # on an up-to-date main, with no local changes
git push -u origin release-v0.1.0     # then open a pull request and merge it
make release-tag VERSION=v0.1.0       # on main, once it's merged
```

`release-prepare` turns the changelog's Unreleased section into `## [0.1.0] - <today>`, under a
new empty Unreleased, sets the OpenAPI spec's `info.version` to `0.1.0`
(`scripts/spec-version.sh`), and commits both on the branch `release-v0.1.0`. It refuses an empty
Unreleased, or a version already released. `release-tag` checks `main` has that section, tags it,
and pushes the tag.

## What the tag publishes

The workflow checks the tag is on a commit that's on `main`, that the changelog has the
version's section, and that `docs/openapi.json` is that version, runs `make check` once more on the tagged commit, then publishes:

| What | Where |
| ---- | ----- |
| The API's image, for linux/amd64 and linux/arm64 | `ghcr.io/ajlatour/golang-vibe-starter`, tagged `v0.1.0`, `0.1` and `latest` |
| An SBOM (SPDX) and build provenance (SLSA v1) for each platform | Attached to the image in the registry, made by BuildKit |
| A GitHub release | Its notes are the version's changelog section, headed by the image's name |

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
makes the same checks and the same multi-platform build, with the SBOM and provenance, and
publishes nothing. `sh scripts/changelog.sh notes` prints what the next release's notes would be.

## Not yet

Images aren't signed. Signing with cosign and keyless OIDC would need a third-party tool, and
GitHub's own artifact attestations aren't available to private repositories on this plan. The
provenance attached to the image records how it was built, but not who vouches for it.
