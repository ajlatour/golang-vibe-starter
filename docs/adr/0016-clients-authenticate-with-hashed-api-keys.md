# ADR-0016: Clients authenticate with hashed API keys

- **Status:** Accepted
- **Date:** 2026-10-05 (f765a76)

**Context.** Anyone who could reach the service could change its data. The options were API keys
checked in the service, JWT or OIDC (a dependency, or a careful implementation), or leaving it to
a gateway in front.

**Decision.** API keys, sent as `Authorization: Bearer`. `API_KEYS` lists each key's name, scope
(read, or read and write) and SHA-256 hash; a request's key is hashed and compared with every
configured hash in constant time. Keys are 256-bit random values from `make api-key`, so a fast
hash is safe. Every route but the probes needs a key; the service refuses to start with none,
unless `API_AUTH=off`, which logs a warning.

**Consequences.** No dependency, and a leaked config holds no usable key (SEC-1, SEC-2). The local
stack ships two documented development keys, which work only on loopback (ADR-0020). Keys are
read once at startup, so rotating one needs a restart. A gateway can still be added in front.
