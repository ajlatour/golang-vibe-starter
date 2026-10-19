# The images the build runs outside compose.yaml and cmd/api/Dockerfile, each
# pinned to a digest. This file is never built: it's here so Dependabot, which
# reads Dockerfiles (.github/dependabot.yml), keeps these pins current too.
# scripts/image.sh prints one by its name (the AS name), for the Makefile and
# the workflows; docs/pins_test.go checks no pinned image is written
# anywhere Dependabot doesn't look.

# The docs site's renderer (make docs).
FROM pandoc/core:3.7@sha256:388e8ed4db8f9586e924f6fed4be88f64cada231777d7f4b8f10a8ae438a7ec9 AS pandoc

# The API spec's linter and renderer (make spec, spec-lint, docs).
FROM redocly/cli:2.57.0@sha256:e320090ecd380878ff2aa671a928d5ac28fcbb7bb6230250d772790811125aea AS redocly

# Linters for the shell scripts and the GitHub workflows (make lint).
FROM koalaman/shellcheck:v0.11.0@sha256:61862eba1fcf09a484ebcc6feea46f1782532571a34ed51fedf90dd25f925a8d AS shellcheck
FROM rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667 AS actionlint

# feature:release
# The release image's SBOM generator (BuildKit's --sbom; the Release workflow).
FROM docker/buildkit-syft-scanner:stable-1@sha256:ae4f3b554449e7e25548e7d8ccc029d17357348e30c6e3df01b92bc93654d6a9 AS syft
# end feature:release
