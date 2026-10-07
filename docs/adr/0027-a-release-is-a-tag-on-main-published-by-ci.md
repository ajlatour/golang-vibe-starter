# ADR-0027: A release is a tag on main, published by CI

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** There were no versions: the changelog was one long Unreleased section, and the image
was built only locally and in CI, never published. A deployment needs a versioned image, and
needs to know what's in it.

**Decision.** A release is a version tag on a commit on `main`. `make release-prepare` cuts the
changelog section in a pull request; `make release-tag` tags `main` once it's merged. The tag
starts a workflow that publishes the image to the GitHub Container Registry for amd64 and arm64,
cross-compiled with no emulation, with an SBOM and build provenance made by BuildKit, and a
GitHub release with the changelog section as its notes. Anything else, a branch or a tag off
`main`, publishes nothing. GitHub's artifact attestations aren't available to private repositories
on this plan, and signing with cosign would need a third-party tool, so images carry provenance
but no signature.

**Consequences.** Versions are reviewed (DEL-1) and their images are reproducible from a commit,
with their contents and build recorded (DEL-2). A dry run rehearses the workflow without
publishing. The SBOM scanner's image is pinned by digest in the workflow, outside what Dependabot
reads, like the pandoc and Redocly images (ADR-0020).
