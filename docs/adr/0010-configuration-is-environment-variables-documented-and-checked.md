# ADR-0010: Configuration is environment variables, documented and checked

- **Status:** Accepted
- **Date:** 2026-10-04 (8c85a5f, 41e1d94)

**Context.** The service runs in containers and on platforms that configure through the
environment. Settings described in several places drift apart.

**Decision.** Every setting is an environment variable with its default in code, read through
`internal/envconfig`, which collects every invalid value before failing. `.env.example` lists every
variable with its default, commented out, and the README has a table of them; a test holds the
code, the file and the table to each other.

**Consequences.** No config files. A typo stops startup with every problem at once (CFG-1). Adding
a setting takes three edits, or a test fails (CFG-2).
