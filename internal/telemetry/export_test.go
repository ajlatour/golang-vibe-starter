package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

// collector is a stand-in OTLP/HTTP receiver, keeping each request it gets.
type collector struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
	body [][]byte
}

func newCollector(t *testing.T, status int) *collector {
	t.Helper()
	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.reqs, c.body = append(c.reqs, r), append(c.body, b)
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(c.Close)
	return c
}

func getenv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// TestExportIsOTLPJSON checks the spans go to the endpoint as OTLP/HTTP's
// JSON encoding: grouped by resource and scope, IDs in hex, 64-bit integers
// as strings, kinds and status codes as OTLP numbers them, and with the
// headers the environment sets.
func TestExportIsOTLPJSON(t *testing.T) {
	c := newCollector(t, http.StatusOK)
	exp, err := newExporter(c.URL+"/v1/traces", getenv(map[string]string{
		"OTEL_EXPORTER_OTLP_HEADERS":        "x-api-key=secret%20one, x-team=a",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS": "x-team=b",
	}))
	if err != nil {
		t.Fatal(err)
	}
	s, err := newSpans(exp, "test-service", "v1")
	if err != nil {
		t.Fatal(err)
	}
	caller, _ := tracectx.Parse("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	server, endServer := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Server, Name: "GET", Method: "GET", Parent: caller})
	_, endClient := s.Start(t.Context(), tracectx.Span{Kind: tracectx.Client, Name: "upstream GET /x", Method: "GET", Parent: server})
	endClient(tracectx.End{Status: 502})
	endServer(tracectx.End{Status: 502, Route: "GET /items"})
	if err := s.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(c.reqs) != 1 {
		t.Fatalf("%d exports, want 1", len(c.reqs))
	}
	r := c.reqs[0]
	if r.Method != http.MethodPost || r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("%s %s, Content-Type %q: want a JSON POST to /v1/traces", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
	}
	if r.Header.Get("X-Api-Key") != "secret one" || r.Header.Get("X-Team") != "b" {
		t.Errorf("headers %v: want the environment's, decoded, the traces' winning", r.Header)
	}

	var got struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []struct {
					Key   string
					Value map[string]any
				}
			}
			ScopeSpans []struct {
				Scope struct{ Name string }
				Spans []struct {
					TraceID, SpanID, ParentSpanID, Name string
					Kind                                int
					StartTimeUnixNano, EndTimeUnixNano  string
					Attributes                          []struct {
						Key   string
						Value map[string]any
					}
					Status struct {
						Code    int
						Message string
					}
				}
			}
		}
	}
	if err := json.Unmarshal(c.body[0], &got); err != nil {
		t.Fatalf("not JSON: %v: %s", err, c.body[0])
	}
	if len(got.ResourceSpans) != 1 || len(got.ResourceSpans[0].ScopeSpans) != 1 {
		t.Fatalf("want one resource with one scope: %s", c.body[0])
	}
	rs := got.ResourceSpans[0]
	var name any
	for _, a := range rs.Resource.Attributes {
		if a.Key == "service.name" {
			name = a.Value["stringValue"]
		}
	}
	if name != "test-service" {
		t.Errorf("service.name %v: %s", name, c.body[0])
	}
	spans := rs.ScopeSpans[0].Spans
	if len(spans) != 2 {
		t.Fatalf("%d spans, want 2: %s", len(spans), c.body[0])
	}
	for _, sp := range spans {
		if sp.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || len(sp.SpanID) != 16 || sp.StartTimeUnixNano == "" || sp.EndTimeUnixNano < sp.StartTimeUnixNano {
			t.Errorf("span %+v: want hex IDs in the trace, and times as strings", sp)
		}
		if sp.Status.Code != 2 {
			t.Errorf("span %s: status %d, want 2, OTLP's error, for a 502", sp.Name, sp.Status.Code)
		}
		for _, a := range sp.Attributes {
			if a.Key == "http.response.status_code" && a.Value["intValue"] != "502" {
				t.Errorf("status code %v, want the string \"502\", as OTLP encodes 64-bit integers", a.Value)
			}
		}
		switch sp.Name {
		case "GET /items":
			if sp.Kind != 2 || sp.ParentSpanID != "00f067aa0ba902b7" {
				t.Errorf("server span: kind %d, parent %s; want 2 and the caller's", sp.Kind, sp.ParentSpanID)
			}
		case "upstream GET /x":
			if sp.Kind != 3 || len(sp.ParentSpanID) != 16 || sp.ParentSpanID == "00f067aa0ba902b7" {
				t.Errorf("client span: kind %d, parent %s; want 3 and the server span", sp.Kind, sp.ParentSpanID)
			}
		default:
			t.Errorf("a span named %q", sp.Name)
		}
	}
}

// TestExportFails checks a collector's refusal, or no collector, is an
// error, and one that never names the URL, which may carry credentials.
func TestExportFails(t *testing.T) {
	refusing := newCollector(t, http.StatusServiceUnavailable)
	if err := mustExporter(t, refusing.URL).ExportSpans(t.Context(), nil); err != nil {
		t.Errorf("exporting nothing: %v", err) // nothing is sent
	}

	exp, err := newExporter("http://user:hunter2@127.0.0.1:1/v1/traces", getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	var errs []error
	for _, e := range []*exporter{exp, mustExporter(t, refusing.URL)} {
		sp, err := newSpans(e, "x", "v1")
		if err != nil {
			t.Fatal(err)
		}
		_, end := sp.Start(t.Context(), tracectx.Span{Kind: tracectx.Server, Parent: tracectx.New()})
		end(tracectx.End{Status: 200})
		errs = append(errs, sp.provider.ForceFlush(t.Context()))
	}
	for i, err := range errs {
		if err == nil {
			t.Errorf("export %d: no error", i)
		} else if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("the error names the URL's password: %v", err)
		}
	}
	if !strings.Contains(errs[1].Error(), "503") {
		t.Errorf("a refusal's error %v doesn't say the status", errs[1])
	}
}

func mustExporter(t *testing.T, endpoint string) *exporter {
	t.Helper()
	e, err := newExporter(endpoint, getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExporterSettings(t *testing.T) {
	for name, tt := range map[string]struct {
		endpoint string
		env      map[string]string
	}{
		"not a URL":           {endpoint: "collector:4318"},
		"no host":             {endpoint: "http:///v1/traces"},
		"a header with no =":  {endpoint: "http://c", env: map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "x-key"}},
		"a timeout of 0":      {endpoint: "http://c", env: map[string]string{"OTEL_EXPORTER_OTLP_TIMEOUT": "0"}},
		"a timeout with unit": {endpoint: "http://c", env: map[string]string{"OTEL_EXPORTER_OTLP_TIMEOUT": "5s"}},
	} {
		if _, err := newExporter(tt.endpoint, getenv(tt.env)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	e, err := newExporter("https://c/v1/traces", getenv(map[string]string{"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT": "2500"}))
	if err != nil || e.client.Timeout.Milliseconds() != 2500 {
		t.Errorf("timeout %v, %v; want 2.5s", e, err)
	}
}
