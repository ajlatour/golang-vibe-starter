# The images the build runs outside compose.yaml and cmd/api/Dockerfile, each
# pinned to a digest. This file is never built: it's here so Dependabot, which
# reads Dockerfiles (.github/dependabot.yml), keeps these pins current too.
# scripts/image.sh prints one by its name (the AS name), for the Makefile and
# the workflows; docs/pins_test.go checks no pinned image is written
# anywhere Dependabot doesn't look.

# The docs site's renderer (make docs).
FROM pandoc/core:3.11@sha256:62518bfb6833705dc90a1634d8d879299bd820b3f74ad3b32fba8b618452553a AS pandoc

# The API spec's linter and renderer (make spec, spec-lint, docs).
FROM redocly/cli:2.72.1@sha256:d39a229ebd8250fea62f44c377ddb04797e8a155140eda49db17f1caaa9334ff AS redocly

# Linters for the shell scripts and the GitHub workflows (make lint).
FROM koalaman/shellcheck:v0.11.0@sha256:61862eba1fcf09a484ebcc6feea46f1782532571a34ed51fedf90dd25f925a8d AS shellcheck
FROM rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667 AS actionlint

# feature:release
# The release image's SBOM generator (BuildKit's --sbom; the Release workflow).
FROM docker/buildkit-syft-scanner:stable-1@sha256:873a7458c3c8ac8e32c7b796c10e1600b07b963d2aa000a72a2d97aaaa2d6315 AS syft
# end feature:release
