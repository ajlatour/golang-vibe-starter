# ADR-0020: CI pins, updates, scans and isolates

- **Status:** Accepted
- **Date:** 2026-10-05 (546210d, b9a93e9)

**Context.** CI runs third-party actions and images, and the performance workflow writes a comment
to the pull request: a token with write access in a job that runs the pull request's code.

**Decision.** Pin every action to a commit SHA and every image to a digest; let Dependabot propose
updates weekly; run govulncheck on every push. Run pull request code only in read-only jobs, and
post the comment from a job that never checks it out. Publish the local stack on loopback only.

**Consequences.** A moved tag can't change what runs (SEC-8). Updates arrive as reviewable pull
requests, each with its own performance report. The pandoc and Redocly digests in the Makefile are
outside what Dependabot reads and are updated by hand.
