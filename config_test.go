package main

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	defaults := config{
		Addr:            ":8080",
		LogLevel:        slog.LevelInfo,
		LogFormat:       "json",
		ShutdownTimeout: 10 * time.Second,
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    config
		wantErr bool
	}{
		{name: "defaults", env: nil, want: defaults},
		{
			name: "all set",
			env: map[string]string{
				"ADDR":             "127.0.0.1:9000",
				"LOG_LEVEL":        "debug",
				"LOG_FORMAT":       "text",
				"SHUTDOWN_TIMEOUT": "30s",
			},
			want: config{
				Addr:            "127.0.0.1:9000",
				LogLevel:        slog.LevelDebug,
				LogFormat:       "text",
				ShutdownTimeout: 30 * time.Second,
			},
		},
		{
			name: "PORT used when ADDR unset",
			env:  map[string]string{"PORT": "3000"},
			want: config{Addr: ":3000", LogLevel: defaults.LogLevel, LogFormat: defaults.LogFormat, ShutdownTimeout: defaults.ShutdownTimeout},
		},
		{
			name: "ADDR wins over PORT",
			env:  map[string]string{"ADDR": ":9000", "PORT": "3000"},
			want: config{Addr: ":9000", LogLevel: defaults.LogLevel, LogFormat: defaults.LogFormat, ShutdownTimeout: defaults.ShutdownTimeout},
		},
		{name: "bad log level", env: map[string]string{"LOG_LEVEL": "loud"}, wantErr: true},
		{name: "bad log format", env: map[string]string{"LOG_FORMAT": "xml"}, wantErr: true},
		{name: "bad shutdown timeout", env: map[string]string{"SHUTDOWN_TIMEOUT": "soon"}, wantErr: true},
		{name: "zero shutdown timeout", env: map[string]string{"SHUTDOWN_TIMEOUT": "0s"}, wantErr: true},
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
			if got != tt.want {
				t.Errorf("loadConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
