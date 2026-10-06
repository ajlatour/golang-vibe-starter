package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantURL     string
		wantTimeout time.Duration
		wantErr     bool
	}{
		{
			name:        "minimal",
			env:         map[string]string{"UPSTREAM_URL": "http://mock:8080"},
			wantURL:     "http://mock:8080",
			wantTimeout: 5 * time.Second,
		},
		{
			name:        "https with path and timeout",
			env:         map[string]string{"UPSTREAM_URL": "https://backend.example.com/v1", "UPSTREAM_TIMEOUT": "2s"},
			wantURL:     "https://backend.example.com/v1",
			wantTimeout: 2 * time.Second,
		},
		{name: "upstream missing", env: nil, wantErr: true},
		{name: "upstream relative", env: map[string]string{"UPSTREAM_URL": "/items"}, wantErr: true},
		{name: "upstream bad scheme", env: map[string]string{"UPSTREAM_URL": "ftp://mock"}, wantErr: true},
		{name: "upstream no host", env: map[string]string{"UPSTREAM_URL": "http://"}, wantErr: true},
		{name: "upstream with query", env: map[string]string{"UPSTREAM_URL": "http://mock?token=abc"}, wantErr: true},
		{name: "upstream with empty query", env: map[string]string{"UPSTREAM_URL": "http://mock/v1?"}, wantErr: true},
		{name: "upstream with fragment", env: map[string]string{"UPSTREAM_URL": "http://mock/v1#x"}, wantErr: true},
		{name: "upstream unparseable", env: map[string]string{"UPSTREAM_URL": "http://[::1"}, wantErr: true},
		{name: "bad upstream timeout", env: map[string]string{"UPSTREAM_URL": "http://mock", "UPSTREAM_TIMEOUT": "0s"}, wantErr: true},
		{name: "shared config error surfaces", env: map[string]string{"UPSTREAM_URL": "http://mock", "LOG_LEVEL": "loud"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadConfig(func(k string) string { return tt.env[k] })
			if tt.wantErr {
				if err == nil {
					t.Fatalf("loadConfig() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadConfig() error = %v", err)
			}
			if got.UpstreamURL.String() != tt.wantURL {
				t.Errorf("UpstreamURL = %q, want %q", got.UpstreamURL, tt.wantURL)
			}
			if got.UpstreamTimeout != tt.wantTimeout {
				t.Errorf("UpstreamTimeout = %v, want %v", got.UpstreamTimeout, tt.wantTimeout)
			}
		})
	}
}

func TestLoadConfigFlags(t *testing.T) {
	base := map[string]string{"UPSTREAM_URL": "http://mock:8080"}
	with := func(k, v string) func(string) string {
		return func(key string) string {
			if key == k {
				return v
			}
			return base[key]
		}
	}

	t.Run("disabled by default", func(t *testing.T) {
		got, err := loadConfig(func(k string) string { return base[k] })
		if err != nil {
			t.Fatal(err)
		}
		if got.FlagsURL != nil {
			t.Errorf("FlagsURL = %v, want nil", got.FlagsURL)
		}
		if got.FlagsTimeout != 500*time.Millisecond {
			t.Errorf("FlagsTimeout = %v, want 500ms", got.FlagsTimeout)
		}
		if got.FlagsCacheTTL != time.Second {
			t.Errorf("FlagsCacheTTL = %v, want 1s", got.FlagsCacheTTL)
		}
	})
	t.Run("cache off", func(t *testing.T) {
		got, err := loadConfig(with("FLAGS_CACHE_TTL", "0s"))
		if err != nil {
			t.Fatal(err)
		}
		if got.FlagsCacheTTL != 0 {
			t.Errorf("FlagsCacheTTL = %v, want 0", got.FlagsCacheTTL)
		}
	})
	t.Run("url and timeout", func(t *testing.T) {
		env := map[string]string{"UPSTREAM_URL": "http://mock:8080", "FLAGS_URL": "http://flagd:8016", "FLAGS_TIMEOUT": "1s"}
		got, err := loadConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if got.FlagsURL == nil || got.FlagsURL.String() != "http://flagd:8016" {
			t.Errorf("FlagsURL = %v, want http://flagd:8016", got.FlagsURL)
		}
		if got.FlagsTimeout != time.Second {
			t.Errorf("FlagsTimeout = %v, want 1s", got.FlagsTimeout)
		}
	})
	for name, getenv := range map[string]func(string) string{
		"relative url":    with("FLAGS_URL", "/ofrep"),
		"bad scheme":      with("FLAGS_URL", "grpc://flagd:8013"),
		"query string":    with("FLAGS_URL", "http://flagd:8016?key=abc"),
		"zero timeout":    with("FLAGS_TIMEOUT", "0s"),
		"garbage timeout": with("FLAGS_TIMEOUT", "soon"),
		"negative cache":  with("FLAGS_CACHE_TTL", "-1s"),
		"garbage cache":   with("FLAGS_CACHE_TTL", "a while"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfig(getenv); err == nil {
				t.Error("loadConfig() error = nil, want error")
			}
		})
	}
}

func TestLoadConfigHeaderGuardMode(t *testing.T) {
	for _, tt := range []struct {
		value   string
		want    headerguard.Mode
		wantErr bool
	}{
		{value: "", want: headerguard.Enforce},
		{value: "enforce", want: headerguard.Enforce},
		{value: "report", want: headerguard.Report},
		{value: "off", wantErr: true},
	} {
		t.Run("HEADER_GUARD_MODE="+tt.value, func(t *testing.T) {
			env := map[string]string{"UPSTREAM_URL": "http://mock", "HEADER_GUARD_MODE": tt.value}
			got, err := loadConfig(func(k string) string { return env[k] })
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "HEADER_GUARD_MODE") {
					t.Errorf("loadConfig() error = %v, want one naming HEADER_GUARD_MODE", err)
				}
				return
			}
			if err != nil || got.HeaderGuard.Mode != tt.want {
				t.Errorf("HeaderGuard.Mode = %v, err = %v; want %v", got.HeaderGuard.Mode, err, tt.want)
			}
		})
	}
}

func TestLoadConfigHeaderLimits(t *testing.T) {
	base := map[string]string{"UPSTREAM_URL": "http://mock"}
	load := func(extra map[string]string) (config, error) {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		return loadConfig(func(k string) string { return env[k] })
	}

	t.Run("defaults are headerguard.DefaultPolicy", func(t *testing.T) {
		got, err := load(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.HeaderGuard, headerguard.DefaultPolicy()) {
			t.Errorf("HeaderGuard = %+v, want the default policy %+v", got.HeaderGuard, headerguard.DefaultPolicy())
		}
	})
	t.Run("overrides", func(t *testing.T) {
		got, err := load(map[string]string{"HEADER_MAX_COUNT": "50", "HEADER_MAX_BYTES": "16384", "HEADER_MAX_TOTAL_BYTES": "65536"})
		if err != nil {
			t.Fatal(err)
		}
		g := got.HeaderGuard
		if g.MaxHeaders != 50 || g.MaxHeaderBytes != 16384 || g.MaxTotalBytes != 65536 {
			t.Errorf("limits = %d, %d, %d; want 50, 16384, 65536", g.MaxHeaders, g.MaxHeaderBytes, g.MaxTotalBytes)
		}
		if !reflect.DeepEqual(g.Strip, headerguard.DefaultPolicy().Strip) {
			t.Error("overriding the limits changed the stripped headers")
		}
	})
	for name, env := range map[string]map[string]string{
		"zero would switch the limit off": {"HEADER_MAX_COUNT": "0"},
		"negative":                        {"HEADER_MAX_BYTES": "-1"},
		"not a number":                    {"HEADER_MAX_TOTAL_BYTES": "32KiB"},
		"above Go's 1 MiB":                {"HEADER_MAX_TOTAL_BYTES": "2000000"},
		"per-header above the total":      {"HEADER_MAX_BYTES": "40000"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(env); err == nil {
				t.Errorf("loadConfig(%v) = nil error; the service must refuse to start", env)
			}
		})
	}
}

func TestLoadConfigRuntimeStatsInterval(t *testing.T) {
	env := func(interval string) func(string) string {
		return func(k string) string {
			switch k {
			case "UPSTREAM_URL":
				return "http://mock:8080"
			case "RUNTIME_STATS_INTERVAL":
				return interval
			}
			return ""
		}
	}
	got, err := loadConfig(env(""))
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeStatsInterval != 5*time.Minute {
		t.Errorf("default = %v, want 5m", got.RuntimeStatsInterval)
	}
	if got, err := loadConfig(env("0s")); err != nil || got.RuntimeStatsInterval != 0 {
		t.Errorf("0s = %v, %v; want off, no error", got.RuntimeStatsInterval, err)
	}
	for _, bad := range []string{"-1m", "often"} {
		if _, err := loadConfig(env(bad)); err == nil {
			t.Errorf("RUNTIME_STATS_INTERVAL=%s: no error, want one", bad)
		}
	}
}

func TestLoadConfigLogClientIP(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			switch k {
			case "UPSTREAM_URL":
				return "http://mock:8080"
			case "LOG_CLIENT_IP":
				return v
			}
			return ""
		}
	}
	for v, want := range map[string]bool{"": true, "on": true, "off": false} {
		got, err := loadConfig(env(v))
		if err != nil || got.LogClientIP != want {
			t.Errorf("LOG_CLIENT_IP=%q: %v, %v; want %v", v, got.LogClientIP, err, want)
		}
	}
	if _, err := loadConfig(env("yes")); err == nil {
		t.Error("LOG_CLIENT_IP=yes: no error, want one")
	}
}

func TestLoadConfigUpstreamRetries(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			switch k {
			case "UPSTREAM_URL":
				return "http://mock:8080"
			case "UPSTREAM_RETRIES":
				return v
			}
			return ""
		}
	}
	for v, want := range map[string]int{"": 1, "0": 0, "3": 3} {
		if got, err := loadConfig(env(v)); err != nil || got.UpstreamRetries != want {
			t.Errorf("UPSTREAM_RETRIES=%q: %d, %v; want %d", v, got.UpstreamRetries, err, want)
		}
	}
	for _, bad := range []string{"-1", "4", "many"} {
		if _, err := loadConfig(env(bad)); err == nil {
			t.Errorf("UPSTREAM_RETRIES=%s: no error, want one", bad)
		}
	}
}
