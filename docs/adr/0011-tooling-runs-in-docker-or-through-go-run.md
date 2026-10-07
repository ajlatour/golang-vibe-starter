# ADR-0011: Tooling runs in Docker or through go run

- **Status:** Accepted
- **Date:** 2026-10-04 (a3eba67)

**Context.** Rendering the docs and the spec would normally need pandoc and Node.js (Redocly), and
load testing k6: each a tool for every contributor to install and keep at the right version.

**Decision.** Run them from pinned Docker images (pandoc, Redocly, k6), and Go tools through
`go run` at a pinned version (benchstat, pkgsite, govulncheck). No Node.js anywhere.

**Consequences.** Go and Docker are all a contributor installs (DEP-4), and everyone runs the same
versions. The first run of a target pulls its image.
