package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestTracing runs the service with an OTLP endpoint, against a stand-in
// collector, and checks it says so at startup and exports the spans of a
// request by the time it's stopped; and that without one, it records none.
func TestTracing(t *testing.T) {
	var exported atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/otlp/v1/traces" {
			exported.Add(1)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	ctx, cancel := context.WithCancel(t.Context())
	addr, logs, errc := startApp(t, ctx, map[string]string{
		"ADDR": "127.0.0.1:0", "STORE": "memory", "API_AUTH": "off",
		"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL + "/otlp",
	})
	resp, err := http.Get("http://" + addr + "/items")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cancel()
	waitForShutdown(t, errc)

	if !strings.Contains(logs.String(), `"msg":"using tracing"`) || !strings.Contains(logs.String(), collector.URL+"/otlp/v1/traces") {
		t.Errorf("no \"using tracing\" with the traces URL; logs: %s", logs)
	}
	if exported.Load() == 0 {
		t.Error("no spans were exported to the collector")
	}

	t.Run("off without an endpoint", func(t *testing.T) {
		cfg, err := loadConfig(getenvFrom(map[string]string{"STORE": "memory"}))
		if err != nil {
			t.Fatal(err)
		}
		spans, stop, err := newSpans(t.Context(), nil, cfg, 0)
		if err != nil || spans != nil {
			t.Errorf("newSpans = %v, %v; want none", spans, err)
		}
		stop()
	})
}

func TestOTLPEndpoint(t *testing.T) {
	for _, tt := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"}, "http://collector:4318/v1/traces"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318/otlp/"}, "http://collector:4318/otlp/v1/traces"},
		{map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://traces.example.com/in"}, "https://traces.example.com/in"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://a:4318", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://b:4318/t"}, "http://b:4318/t"},
	} {
		tt.env["STORE"] = "memory"
		cfg, err := loadConfig(getenvFrom(tt.env))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.TracesEndpoint.String(); got != tt.want {
			t.Errorf("%v: endpoint %s, want %s", tt.env, got, tt.want)
		}
	}
	if _, err := loadConfig(getenvFrom(map[string]string{"STORE": "memory", "OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4318"})); err == nil {
		t.Error("an endpoint that isn't a URL: no error")
	}
}
