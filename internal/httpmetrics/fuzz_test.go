package httpmetrics_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpmetrics"
)

// FuzzObserve records requests with arbitrary routes and methods: the output
// stays valid Prometheus text, every value quoted safely, and the method
// label stays within the known set.
func FuzzObserve(f *testing.F) {
	for _, seed := range []struct{ route, method string }{
		{"GET /items", "GET"}, {"", "X-ODD"}, {`GET /a"b`, "POST"}, {"GET /a\nb", "BREW"}, {`\`, ""},
	} {
		f.Add(seed.route, seed.method, 200)
	}
	sample := regexp.MustCompile(`^[a-z_]+(\{(route="(\\.|[^"\\])*",method="(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS|OTHER)",status="-?\d+"(,le="[^"]+")?)\})? \S+$`)
	f.Fuzz(func(t *testing.T, route, method string, status int) {
		rec := httpmetrics.New()
		rec.Start()
		rec.Observe(route, method, status, time.Millisecond)
		var b strings.Builder
		rec.WritePrometheus(&b)
		for line := range strings.SplitSeq(strings.TrimSuffix(b.String(), "\n"), "\n") {
			if strings.HasPrefix(line, "# ") {
				continue
			}
			if !sample.MatchString(line) {
				t.Fatalf("a line Prometheus couldn't parse: %q", line)
			}
		}
	})
}
