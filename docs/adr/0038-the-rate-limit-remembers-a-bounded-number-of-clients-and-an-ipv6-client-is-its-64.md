# ADR-0038: The rate limit remembers a bounded number of clients, and an IPv6 client is its /64

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** The per-client rate limit (ADR-0018) keeps a token bucket per client, and forgets the
idle ones once a minute. A client without a key is its IP address, and with IPv6, one subscriber
typically has a /64 to move around in for free: by changing address on every request, it could
take a fresh limit each time, escaping the limit, and fill memory with buckets between sweeps.

**Decision.** An IPv6 client is its /64; an IPv4 client, its address, as before. The limiter
remembers at most `MaxClients` (100,000, about 10 MB) at once: when it's full, it forgets the idle
clients at once rather than waiting for the minute's sweep, and if none are idle, new clients share
one limit, the overflow, until there's room.

**Consequences.** Memory for the limit is bounded, whatever the traffic. Clients sharing a /64
share a limit, which a network that gives many users one /64 would feel. In a flood from more
than 100,000 active addresses, newcomers are limited together, which throttles the flood rather
than each of its addresses; clients the limiter already knows keep their own limits.
