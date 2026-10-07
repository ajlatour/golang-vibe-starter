# ADR-0026: Static analysis is gosec and staticcheck, not CodeQL

- **Status:** Accepted
- **Date:** 2026-10-05

**Context.** Tests, fuzzing, `go vet` and govulncheck catch a lot, but not every security mistake or
suspicious construct. CodeQL, GitHub's analysis, was the first choice; on a private repository it
needs GitHub Advanced Security, which this repository doesn't have, and its licence allows it on
private code only with that.

**Decision.** Run two open-source analysers for Go through `go run` at pinned versions (ADR-0011):
staticcheck, for bugs and suspicious code, and gosec, for security mistakes, on all three modules,
as `make lint` and a CI job. A finding is fixed, or accepted on its own line with the rule and the
reason (`// #nosec G304 -- why`). The report tool, whose job is reading the files named on its
command line, skips gosec's file-inclusion rule.

**Consequences.** No licence or setting needed (SEC-9). Their first run found a fragile test
condition, an unchecked hex decode in `apikey.ParseKeys`, and five unhandled `Close` errors, all
fixed, plus one false positive accepted. Results live in the CI log, not GitHub's Security tab. If
the repository becomes public or gains Advanced Security, CodeQL can be added beside them.
