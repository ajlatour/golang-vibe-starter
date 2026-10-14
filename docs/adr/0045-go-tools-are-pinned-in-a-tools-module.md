# ADR-0045: Go tools are pinned in a tools module

- **Status:** Accepted
- **Date:** 2026-10-14

**Context.** The Go tools CI runs (staticcheck, gosec, modernize, govulncheck and benchstat) ran
as `go run tool@version` ([ADR-0011](0011-tooling-runs-in-docker-or-through-go-run.md)). The
version was pinned, but each run fetched the tool and its dependencies, about 80 modules, from
the module proxy unless the job's cache happened to hold them. The jobs that share a cache key
save whichever finishes first, so the lint job's tools were often fetched afresh, and a lint run
failed once on a dropped connection to the proxy: a failure that had nothing to do with the
change. Their dependencies' versions weren't recorded anywhere, and Dependabot couldn't see the
tools to update them.

**Decision.** The Go tools are `tool` directives in a module of their own, `tools/go.mod`, with
every dependency's checksum in `tools/go.sum`. `make tools` builds them into `bin/tools`, and
`make lint`, `make vuln` and `make bench-compare` run the binaries, in each module. The lint and
vuln jobs key their cache on `tools/go.sum`, and both build every tool, so the cache they save
holds all of them. Dependabot watches `tools/` like the other modules. Tools that aren't Go, or
that only run locally (pkgsite, for `make godoc`), stay as they were.

**Consequences.** A cached CI run fetches nothing for its tools, and builds them from the build
cache in seconds. A tool's update is a reviewed pull request, with its dependencies' checksums.
The tools share one dependency graph, so two needing different versions of a shared module (both
use `golang.org/x/tools`) get the higher, which their tests can't vouch for; a tool that breaks
that way is moved to a module of its own.
