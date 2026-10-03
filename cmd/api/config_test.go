package main

import (
	"testing"
	"time"
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
		"zero timeout":    with("FLAGS_TIMEOUT", "0s"),
		"garbage timeout": with("FLAGS_TIMEOUT", "soon"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfig(getenv); err == nil {
				t.Error("loadConfig() error = nil, want error")
			}
		})
	}
}
