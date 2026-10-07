package profiling

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// The cgroup files that hold a container's memory limit: v2's, then v1's.
const (
	cgroupV2MemoryMax = "/sys/fs/cgroup/memory.max"
	cgroupV1MemoryMax = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
)

// memoryLimitShare is the part of the container's memory the Go runtime
// aims to stay under: the rest is for what it doesn't count (its own
// stacks and buffers outside the heap's accounting, the binary, the
// kernel's buffers for this process), so the container's limit, and the
// kernel's OOM killer, are reached only if the program really needs more.
const memoryLimitShare = 0.9

// Where the memory limit came from, as "using runtime" reports it.
const (
	MemoryLimitFromEnv       = "GOMEMLIMIT"
	MemoryLimitFromContainer = "container"
	MemoryLimitNone          = "none"
)

// LimitMemory gives the Go runtime a soft memory limit from the container's,
// when GOMEMLIMIT doesn't set one: 90% of the cgroup's memory limit. The Go
// runtime doesn't read the cgroup itself, so without a limit it collects
// garbage by GOGC alone, and the heap can grow past the container's limit
// between collections: the kernel then kills the process, dropping every
// request in flight, with no log line. With one, the collector works harder
// as memory nears it. It returns where the limit came from: GOMEMLIMIT, the
// container, or none (outside a container, or one without a limit).
func LimitMemory(getenv func(string) string) string {
	if getenv("GOMEMLIMIT") != "" {
		return MemoryLimitFromEnv // the runtime has read it already
	}
	limit, ok := cgroupMemoryLimit(os.ReadFile)
	if !ok {
		return MemoryLimitNone
	}
	debug.SetMemoryLimit(int64(float64(limit) * memoryLimitShare))
	return MemoryLimitFromContainer
}

// cgroupMemoryLimit reads the container's memory limit, in bytes, from
// cgroup v2's memory.max or v1's memory.limit_in_bytes. It reports false
// when there's no limit: no such file (not a container, or cgroups not
// mounted there), v2's "max", or v1's way of saying none, a number near
// the largest an int64 holds.
func cgroupMemoryLimit(read func(string) ([]byte, error)) (int64, bool) {
	for _, path := range []string{cgroupV2MemoryMax, cgroupV1MemoryMax} {
		b, err := read(path)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(b))
		if v == "max" {
			return 0, false
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n >= 1<<62 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
