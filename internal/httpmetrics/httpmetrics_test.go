package httpmetrics_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpmetrics"
)

func scrape(rec *httpmetrics.Recorder) string {
	var b strings.Builder
	rec.WritePrometheus(&b)
	return b.String()
}

func contains(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestEmpty(t *testing.T) {
	// The names are visible before any request, so dashboards and checks can
	// rely on them.
	contains(t, scrape(httpmetrics.New()),
		"# TYPE http_requests_total counter\n",
		"# TYPE http_request_duration_seconds histogram\n",
		"# TYPE http_requests_in_flight gauge\nhttp_requests_in_flight 0\n",
		"# TYPE http_requests_rejected_total counter\nhttp_requests_rejected_total 0\n")
}

func TestRejected(t *testing.T) {
	rec := httpmetrics.New()
	rec.Rejected()
	rec.Rejected()
	contains(t, scrape(rec), "http_requests_rejected_total 2\n")
}

func TestObserve(t *testing.T) {
	rec := httpmetrics.New()
	for _, d := range []time.Duration{500 * time.Microsecond, 3 * time.Millisecond, 3 * time.Millisecond, 20 * time.Second} {
		rec.Start()
		rec.Observe("GET /items/{id}", "GET", 200, d)
	}
	rec.Start()
	rec.Observe("GET /items/{id}", "GET", 404, time.Millisecond)
	rec.Start() // still in flight

	l := `route="GET /items/{id}",method="GET",status="200"`
	contains(t, scrape(rec),
		`http_requests_total{`+l+`} 4`,
		`http_requests_total{route="GET /items/{id}",method="GET",status="404"} 1`,
		`http_request_duration_seconds_bucket{`+l+`,le="0.001"} 1`,
		`http_request_duration_seconds_bucket{`+l+`,le="0.005"} 3`,
		`http_request_duration_seconds_bucket{`+l+`,le="10"} 3`,
		`http_request_duration_seconds_bucket{`+l+`,le="+Inf"} 4`,
		`http_request_duration_seconds_sum{`+l+`} 20.0065`,
		`http_request_duration_seconds_count{`+l+`} 4`,
		"http_requests_in_flight 1\n")
}

// TestLabelsAreBounded checks no request can create series at will: the route
// is the matched pattern or "none", and odd methods share one label.
func TestLabelsAreBounded(t *testing.T) {
	rec := httpmetrics.New()
	for i := range 100 {
		rec.Start()
		rec.Observe("", fmt.Sprintf("X-METHOD-%d", i), 404, time.Millisecond)
	}
	out := scrape(rec)
	if n := strings.Count(out, "http_requests_total{"); n != 1 {
		t.Errorf("100 unrouted requests with made-up methods made %d series, want 1:\n%s", n, out)
	}
	contains(t, out, `http_requests_total{route="none",method="OTHER",status="404"} 100`)
}

func TestNoAllocationsPerRequest(t *testing.T) {
	rec := httpmetrics.New()
	rec.Start()
	rec.Observe("GET /items", "GET", 200, time.Millisecond)
	if n := testing.AllocsPerRun(100, func() {
		rec.Start()
		rec.Observe("GET /items", "GET", 200, time.Millisecond)
	}); n != 0 {
		t.Errorf("recording a request allocates %v times, want 0", n)
	}
}

func TestConcurrent(t *testing.T) {
	rec := httpmetrics.New()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for range 1000 {
				rec.Start()
				rec.Observe(fmt.Sprintf("GET /r%d", g%2), "GET", 200, time.Millisecond)
				_ = scrape(rec)
			}
		})
	}
	wg.Wait()
	contains(t, scrape(rec), `http_requests_total{route="GET /r0",method="GET",status="200"} 4000`, "http_requests_in_flight 0\n")
}
