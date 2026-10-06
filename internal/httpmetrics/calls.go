package httpmetrics

import (
	"bufio"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// Calls records the calls a service makes to another, for Prometheus: how
// many, by outcome, and how long those that were made took. With prefix
// "validator" it writes
//
//	validator_calls_total{outcome="..."}   one series per outcome
//	validator_call_duration_seconds        a histogram, with Buckets
//
// The outcomes are fixed at construction, so no input can add a series.
type Calls struct {
	prefix, what string
	outcomes     []string
	counts       []atomic.Uint64 // per outcome, in order
	durations    *series
}

// NewCalls returns a recorder for calls described by what (e.g. "Calls to
// the request-validation service"), named with prefix, counting each of
// outcomes.
func NewCalls(prefix, what string, outcomes ...string) *Calls {
	return &Calls{prefix: prefix, what: what, outcomes: outcomes, counts: make([]atomic.Uint64, len(outcomes)), durations: newSeries(CallBuckets)}
}

// CallBuckets are the call histogram's upper bounds, in seconds. Finer at
// the low end than a request's (Buckets): a call to a nearby service takes
// a fraction of a millisecond, and with 1ms as the first bound every such
// call would land in one bucket, leaving its median and p99 to guesswork.
// Up to a second, past any sensible timeout for a call on the request path.
var CallBuckets = []float64{0.0001, 0.00025, 0.0005, 0.00075, 0.001, 0.0015, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}

// Observe records one call's outcome and, if it was made (d > 0), its
// duration. An outcome not given to NewCalls is ignored, duration and all.
func (c *Calls) Observe(outcome string, d time.Duration) {
	for i, o := range c.outcomes {
		if o != outcome {
			continue
		}
		c.counts[i].Add(1)
		if d > 0 {
			c.durations.observe(d)
		}
		return
	}
}

// WritePrometheus writes the counts and the histogram in Prometheus's text
// format, every outcome included before any call, so the names are always
// visible.
func (c *Calls) WritePrometheus(w io.Writer) {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	fmt.Fprintf(bw, "# HELP %s_calls_total %s, by outcome.\n# TYPE %s_calls_total counter\n", c.prefix, c.what, c.prefix)
	for i, o := range c.outcomes {
		fmt.Fprintf(bw, "%s_calls_total{outcome=%q} %d\n", c.prefix, o, c.counts[i].Load())
	}
	name := c.prefix + "_call_duration_seconds"
	fmt.Fprintf(bw, "# HELP %s How long the calls that were made took.\n# TYPE %s histogram\n", name, name)
	c.durations.writeHistogram(bw, name, "")
}
