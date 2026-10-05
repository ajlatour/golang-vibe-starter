// Package monitoring holds the local monitoring stack's configuration
// (Prometheus, and Grafana's data source and dashboard), which compose.yaml's
// monitoring profile mounts. Its only Go code is this test, which keeps that
// configuration in step with the code it watches, without running any of it.
package monitoring

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpmetrics"
	"github.com/ajlatour/golang-vibe-starter/internal/profiling"
)

type dashboard struct {
	Panels []struct {
		Title      string
		Type       string
		Datasource struct{ UID string }
		Targets    []struct {
			Expr       string
			Datasource struct{ UID string }
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func loadDashboard(t *testing.T) dashboard {
	t.Helper()
	var d dashboard
	if err := json.Unmarshal([]byte(readFile(t, "grafana/dashboards/performance.json")), &d); err != nil {
		t.Fatalf("performance.json: %v", err)
	}
	if len(d.Panels) == 0 {
		t.Fatal("the dashboard has no panels")
	}
	return d
}

// exprs returns every query on the dashboard, with its panel's title.
func (d dashboard) exprs() map[string]string {
	out := map[string]string{}
	for _, p := range d.Panels {
		for _, q := range p.Targets {
			out[q.Expr] = p.Title
		}
	}
	return out
}

// TestDashboardUsesTheDataSource fails if a panel names a data source other
// than the one Grafana is provisioned with.
func TestDashboardUsesTheDataSource(t *testing.T) {
	m := regexp.MustCompile(`(?m)^\s*uid:\s*(\S+)`).FindStringSubmatch(readFile(t, "grafana/provisioning/datasources/prometheus.yml"))
	if m == nil {
		t.Fatal("the data source provisioning has no uid")
	}
	for _, p := range loadDashboard(t).Panels {
		if p.Type == "row" {
			continue
		}
		if p.Datasource.UID != m[1] {
			t.Errorf("panel %q uses data source %q, want %q", p.Title, p.Datasource.UID, m[1])
		}
		for _, q := range p.Targets {
			if q.Datasource.UID != m[1] {
				t.Errorf("a query on panel %q uses data source %q, want %q", p.Title, q.Datasource.UID, m[1])
			}
		}
	}
}

// TestDashboardMetricsExist fails if the dashboard queries a Go runtime,
// process or request metric that the API's /metrics doesn't serve, so a renamed metric
// can't leave a panel quietly empty.
func TestDashboardMetricsExist(t *testing.T) {
	rec := httptest.NewRecorder()
	profiling.MetricsHandler(httpmetrics.New().WritePrometheus).ServeHTTP(rec, httptest.NewRequest("GET", profiling.MetricsPath, nil))
	body, _ := io.ReadAll(rec.Body)
	served := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^# TYPE (\S+) (\S+)`).FindAllStringSubmatch(string(body), -1) {
		served[m[1]] = true
		if m[2] == "histogram" { // its samples are <name>_bucket, _sum and _count
			for _, suffix := range []string{"_bucket", "_sum", "_count"} {
				served[m[1]+suffix] = true
			}
		}
	}

	used := regexp.MustCompile(`\b(?:go|process|http)_[a-z0-9_]+`)
	checked := 0
	for expr, panel := range loadDashboard(t).exprs() {
		for _, name := range used.FindAllString(expr, -1) {
			checked++
			if !served[name] {
				t.Errorf("panel %q queries %s, which /metrics doesn't serve", panel, name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no Go runtime metrics on the dashboard; the test is broken")
	}
}

// TestDashboardK6Percentiles fails if the dashboard plots a k6 percentile
// that perf/run.sh doesn't ask k6 to send.
func TestDashboardK6Percentiles(t *testing.T) {
	m := regexp.MustCompile(`K6_PROMETHEUS_RW_TREND_STATS=([^'"\s]+)`).FindStringSubmatch(readFile(t, "../perf/run.sh"))
	if m == nil {
		t.Fatal("perf/run.sh doesn't set K6_PROMETHEUS_RW_TREND_STATS")
	}
	sent := map[string]bool{}
	for _, stat := range strings.Split(m[1], ",") {
		// k6 names p(99) as _p99.
		sent[strings.NewReplacer("(", "", ")", "", ".", "").Replace(stat)] = true
	}
	for expr, panel := range loadDashboard(t).exprs() {
		for _, s := range regexp.MustCompile(`k6_[a-z_]+_(p\d+|max|min|avg|med)\b`).FindAllStringSubmatch(expr, -1) {
			if !sent[s[1]] {
				t.Errorf("panel %q plots %s, but perf/run.sh's K6_PROMETHEUS_RW_TREND_STATS doesn't include it", panel, s[0])
			}
		}
	}
}

// TestPrometheusScrapesTheAPI fails if Prometheus's target for the API isn't
// the address compose serves its metrics on (PPROF_ADDR).
func TestPrometheusScrapesTheAPI(t *testing.T) {
	addr := regexp.MustCompile(`PPROF_ADDR:\s*\S*:(\d+)`).FindStringSubmatch(readFile(t, "../compose.yaml"))
	if addr == nil {
		t.Fatal("compose.yaml doesn't set PPROF_ADDR for the api service")
	}
	if want := `"api:` + addr[1] + `"`; !strings.Contains(readFile(t, "prometheus.yml"), want) {
		t.Errorf("prometheus.yml doesn't scrape %s, where compose serves the API's metrics", want)
	}
}
