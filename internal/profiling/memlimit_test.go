package profiling

import (
	"io/fs"
	"math"
	"runtime/debug"
	"testing"
)

func TestCgroupMemoryLimit(t *testing.T) {
	files := func(m map[string]string) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if v, ok := m[p]; ok {
				return []byte(v), nil
			}
			return nil, fs.ErrNotExist
		}
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  int64
		ok    bool
	}{
		{"cgroup v2 limit", map[string]string{cgroupV2MemoryMax: "536870912\n"}, 512 << 20, true},
		{"cgroup v2 unlimited", map[string]string{cgroupV2MemoryMax: "max\n"}, 0, false},
		{"cgroup v1 limit", map[string]string{cgroupV1MemoryMax: "268435456\n"}, 256 << 20, true},
		{"cgroup v1 unlimited", map[string]string{cgroupV1MemoryMax: "9223372036854771712\n"}, 0, false},
		{"v2 first", map[string]string{cgroupV2MemoryMax: "1048576", cgroupV1MemoryMax: "2097152"}, 1 << 20, true},
		{"not a container", nil, 0, false},
		{"unreadable value", map[string]string{cgroupV2MemoryMax: "lots"}, 0, false},
		{"zero", map[string]string{cgroupV2MemoryMax: "0"}, 0, false},
	} {
		got, ok := cgroupMemoryLimit(files(tc.files))
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: %d, %v; want %d, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// With GOMEMLIMIT set, the runtime has the limit already: LimitMemory
// leaves it alone.
func TestLimitMemoryLeavesGOMEMLIMIT(t *testing.T) {
	before := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(before)
	debug.SetMemoryLimit(math.MaxInt64)
	if got := LimitMemory(func(k string) string { return map[string]string{"GOMEMLIMIT": "1GiB"}[k] }); got != MemoryLimitFromEnv {
		t.Errorf("source = %q, want GOMEMLIMIT", got)
	}
	if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
		t.Errorf("the limit changed to %d", got)
	}
}
