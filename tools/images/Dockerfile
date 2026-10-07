# The images the build runs outside compose.yaml and cmd/api/Dockerfile, each
# pinned to a digest. This file is never built: it's here so Dependabot, which
# reads Dockerfiles (.github/dependabot.yml), keeps these pins current too.
# scripts/image.sh prints one by its name (the AS name), for the Makefile and
# the Release workflow; docs/pins_test.go checks no pinned image is written
# anywhere Dependabot doesn't look.

# The docs site's renderer (make docs).
FROM pandoc/core:3.11@sha256:62518bfb6833705dc90a1634d8d879299bd820b3f74ad3b32fba8b618452553a AS pandoc

# The API spec's linter and renderer (make spec, spec-lint, docs).
FROM redocly/cli:2.57.0@sha256:e320090ecd380878ff2aa671a928d5ac28fcbb7bb6230250d772790811125aea AS redocly

# The release image's SBOM generator (BuildKit's --sbom; the Release workflow).
FROM docker/buildkit-syft-scanner:stable-1@sha256:ae4f3b554449e7e25548e7d8ccc029d17357348e30c6e3df01b92bc93654d6a9 AS syft
