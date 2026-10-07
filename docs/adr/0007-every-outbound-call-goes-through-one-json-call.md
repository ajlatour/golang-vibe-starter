# ADR-0007: Every outbound call goes through one JSON call

- **Status:** Accepted
- **Date:** 2026-10-04 (fa76e63)

**Context.** The store and the flags client each encoded, sent, capped, decoded and classified
their failures, slightly differently.

**Decision.** `internal/jsoncall` makes every outbound JSON call: it encodes, sends, caps and
drains the response, checks the status, decodes, redacts URLs in errors, and classifies failures.
Each client supplies only what differs.

**Consequences.** A rule for all outbound calls lives in one place: the response cap, error
redaction, and later the refusal to follow redirects (ADR-0019).
