# ADR-0006: Logs are structured and held to a contract

- **Status:** Accepted
- **Date:** 2026-10-03 (8ab7b3f, be75964, d68b8aa)

**Context.** Logs are only useful to query and alert on if their messages, levels and fields are
stable. Free-form log lines drift with every change.

**Decision.** Log with `log/slog`, JSON by default, one line per record. Every field name is a
constant in `internal/logx/keys.go`; every message is in `logtest.Contract` with its levels and
required fields, and in the tables of `docs/logging.md`. Every request gets a correlation ID,
logged as `request_id` and returned in `Client-Correlation-Id`.

**Consequences.** A test fails if the code, the contract and the docs disagree, or if a running
service writes a line the contract doesn't allow (OBS-1). Adding a log line takes three edits.
Secrets in URLs are redacted, and a test plants secrets to prove none is logged.
