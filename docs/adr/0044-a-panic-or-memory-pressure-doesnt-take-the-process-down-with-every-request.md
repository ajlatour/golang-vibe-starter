# ADR-0044: A panic or memory pressure doesn't take the process down with every request

- **Status:** Accepted
- **Date:** 2026-10-06

**Context.** Some failures can't be answered from inside the process, because they end it: every
request in flight is dropped, and nothing is logged but the runtime's own text on stderr. Two of
them the service could make less likely. A panic in a goroutine that doesn't recover it ends the
process, and three background tasks ran without one: reloading the API keys, the runtime stats,
and the profiling server. And the Go runtime doesn't read a container's memory limit: without
`GOMEMLIMIT`, it collects garbage by `GOGC` alone, so the heap can grow past the container's limit
between collections, and the kernel kills the process. Neither the image nor the compose stack
set `GOMEMLIMIT`.

**Decision.** Background tasks start through `logx.Go`, which recovers a panic, logs it (`panic
in background task`, at `ERROR`, with the task and its stack) and ends only that task: the service
carries on without it, which the line says, so it's worth an alert. Request handlers already
recover theirs (`httpx.Middleware`). At startup, unless `GOMEMLIMIT` is set, the runtime's soft
memory limit is set to 90% of the container's, read from its cgroup (v2's `memory.max`, else v1's
`memory.limit_in_bytes`), leaving the rest for memory the runtime doesn't count; `using runtime`
logs the limit and where it came from.

**Consequences.** A bug in a background task costs that task, not every request. Near its limit,
the service spends more CPU on garbage collection instead of being killed; a real need for more
memory than the container has still ends in the kernel killing it, which only a bigger limit, or
less use, fixes. What's still out of reach: fatal runtime errors (concurrent map writes, a stack
overflow, running out of memory outright) end the process whatever it does, and those are for the
platform: restart alerts, and the load balancer's own log of the requests it lost.
