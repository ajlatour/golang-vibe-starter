# ADR-0025: CI keeps Go's build cache, not Docker's layers

- **Status:** Accepted
- **Date:** 2026-10-05 (80c5283)

**Context.** CI spent most of the image build compiling from an empty Go build cache. Docker's
layer cache (through `docker/bake-action`) wouldn't help: nearly every pull request changes Go
code, which invalidates the compile layer.

**Decision.** Keep Go's own build cache between runs with `actions/cache`, fed into the image
build as an optional build context and exported after it (`scripts/build-api-image.sh`).

**Consequences.** Only changed packages recompile: the integration job's image build went from 40s
to 29s. Local builds are unchanged. The cache is keyed on the Dockerfile and `go.mod`, so a new Go
version starts afresh rather than growing the old cache.
