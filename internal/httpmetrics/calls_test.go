package httpmetrics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpmetrics"
)

func TestCalls(t *testing.T) {
	c := httpmetrics.NewCalls("validator", "Calls to the request-validation service", "allowed", "refused", "not_asked")
	c.Observe("allowed", 3*time.Millisecond)
	c.Observe("allowed", 40*time.Millisecond)
	c.Observe("refused", 2*time.Millisecond)
	c.Observe("not_asked", 0)         // counted, not timed: no call was made
	c.Observe("made-up", time.Second) // not an outcome: ignored, duration and all
	var out strings.Builder
	c.WritePrometheus(&out)
	got := out.String()
	for _, want := range []string{
		"# TYPE validator_calls_total counter\n",
		`validator_calls_total{outcome="allowed"} 2` + "\n",
		`validator_calls_total{outcome="refused"} 1` + "\n",
		`validator_calls_total{outcome="not_asked"} 1` + "\n",
		"# TYPE validator_call_duration_seconds histogram\n",
		`validator_call_duration_seconds_bucket{le="0.0025"} 1` + "\n",
		`validator_call_duration_seconds_bucket{le="0.005"} 2` + "\n",
		`validator_call_duration_seconds_bucket{le="0.05"} 3` + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "made-up") {
		t.Error("an outcome not given to NewCalls made a series")
	}
	if !strings.Contains(got, "validator_call_duration_seconds_count 3\n") {
		t.Errorf("want 3 calls timed (the not-asked one and the unknown outcome aren't):\n%s", got)
	}
}

// TestCallsBeforeAnyCall checks the names are visible before any call, with
// zeros, so a dashboard or the perf report can rely on them.
func TestCallsBeforeAnyCall(t *testing.T) {
	var out strings.Builder
	httpmetrics.NewCalls("validator", "Calls", "allowed").WritePrometheus(&out)
	for _, want := range []string{`validator_calls_total{outcome="allowed"} 0`, `validator_call_duration_seconds_count 0`, `validator_call_duration_seconds_sum 0`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}
