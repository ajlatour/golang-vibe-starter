// Package httpmetrics counts the requests a service serves and times them,
// for Prometheus: the rate, errors and duration of every route. It is the
// standard library alone, written in Prometheus's text format.
//
// Labels are kept to a bounded set, so no request can make the service hold
// more series: a route is the pattern it matched ("GET /items/{id}", never the
// raw path), "none" for a request answered before routing (refused by the
// header guard, authentication or the limits, or matching no route), and a
// method outside the usual ones counts as OTHER.
package httpmetrics

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Buckets are the latency histogram's upper bounds, in seconds: fine at the
// low end, where a healthy API lives, and up to the upstream timeout.
var Buckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type key struct {
	route, method string
	status        int
}

// series is one route, method and status: a request count and a latency
// histogram, updated without locking.
type series struct {
	buckets []atomic.Uint64 // per bucket, not cumulative; the last is +Inf
	count   atomic.Uint64
	sumBits atomic.Uint64 // float64 seconds, as bits
}

// Recorder records requests. Its zero value isn't usable; use New.
type Recorder struct {
	mu       sync.RWMutex
	series   map[key]*series
	inFlight atomic.Int64
}

// New returns an empty Recorder.
func New() *Recorder {
	return &Recorder{series: map[key]*series{}}
}

var methods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true}

// Start notes a request has begun; Observe notes it has ended.
func (rec *Recorder) Start() { rec.inFlight.Add(1) }

// Observe records a finished request: the route pattern it matched (""
// before routing), its method, the status sent and how long it took.
func (rec *Recorder) Observe(route, method string, status int, d time.Duration) {
	rec.inFlight.Add(-1)
	if route == "" {
		route = "none"
	}
	if !methods[method] {
		method = "OTHER"
	}
	k := key{route, method, status}

	rec.mu.RLock()
	s := rec.series[k]
	rec.mu.RUnlock()
	if s == nil {
		rec.mu.Lock()
		if s = rec.series[k]; s == nil {
			s = &series{buckets: make([]atomic.Uint64, len(Buckets)+1)}
			rec.series[k] = s
		}
		rec.mu.Unlock()
	}

	secs := d.Seconds()
	i, _ := slices.BinarySearch(Buckets, secs)
	s.buckets[i].Add(1)
	s.count.Add(1)
	for {
		old := s.sumBits.Load()
		if s.sumBits.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+secs)) {
			break
		}
	}
}

// WritePrometheus writes the metrics in Prometheus's text format. The HELP and
// TYPE lines are written even before any request, so the metrics' names are
// always visible.
func (rec *Recorder) WritePrometheus(w io.Writer) {
	rec.mu.RLock()
	keys := make([]key, 0, len(rec.series))
	for k := range rec.series {
		keys = append(keys, k)
	}
	all := make(map[key]*series, len(keys))
	for _, k := range keys {
		all[k] = rec.series[k]
	}
	rec.mu.RUnlock()
	slices.SortFunc(keys, func(a, b key) int {
		if c := strings.Compare(a.route, b.route); c != 0 {
			return c
		}
		if c := strings.Compare(a.method, b.method); c != 0 {
			return c
		}
		return a.status - b.status
	})

	bw := bufio.NewWriter(w)
	defer bw.Flush()
	labels := func(k key) string {
		return fmt.Sprintf("route=%q,method=%q,status=\"%d\"", k.route, k.method, k.status)
	}

	fmt.Fprint(bw, "# HELP http_requests_total Requests served, by route pattern, method and status.\n# TYPE http_requests_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(bw, "http_requests_total{%s} %d\n", labels(k), all[k].count.Load())
	}

	fmt.Fprint(bw, "# HELP http_request_duration_seconds How long requests took to serve, by route pattern, method and status.\n# TYPE http_request_duration_seconds histogram\n")
	for _, k := range keys {
		s, l := all[k], labels(k)
		var cumulative uint64
		for i, le := range Buckets {
			cumulative += s.buckets[i].Load()
			fmt.Fprintf(bw, "http_request_duration_seconds_bucket{%s,le=%q} %d\n", l, strconv.FormatFloat(le, 'g', -1, 64), cumulative)
		}
		cumulative += s.buckets[len(Buckets)].Load()
		fmt.Fprintf(bw, "http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", l, cumulative)
		fmt.Fprintf(bw, "http_request_duration_seconds_sum{%s} %s\n", l, strconv.FormatFloat(math.Float64frombits(s.sumBits.Load()), 'g', -1, 64))
		fmt.Fprintf(bw, "http_request_duration_seconds_count{%s} %d\n", l, cumulative)
	}

	fmt.Fprintf(bw, "# HELP http_requests_in_flight Requests being served right now.\n# TYPE http_requests_in_flight gauge\nhttp_requests_in_flight %d\n", rec.inFlight.Load())
}
