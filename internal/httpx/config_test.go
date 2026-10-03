package httpx

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadServerConfig(t *testing.T) {
	defaults := ServerConfig{
		Addr:            ":8080",
		LogLevel:        slog.LevelInfo,
		LogFormat:       "json",
		ShutdownTimeout: 10 * time.Second,
	}
	withAddr := func(addr string) ServerConfig {
		c := defaults
		c.Addr = addr
		return c
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    ServerConfig
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
			want: ServerConfig{
				Addr:            "127.0.0.1:9000",
				LogLevel:        slog.LevelDebug,
				LogFormat:       "text",
				ShutdownTimeout: 30 * time.Second,
			},
		},
		{name: "PORT used when ADDR unset", env: map[string]string{"PORT": "3000"}, want: withAddr(":3000")},
		{name: "ADDR wins over PORT", env: map[string]string{"ADDR": ":9000", "PORT": "3000"}, want: withAddr(":9000")},
		{name: "bad log level", env: map[string]string{"LOG_LEVEL": "loud"}, wantErr: true},
		{name: "bad log format", env: map[string]string{"LOG_FORMAT": "xml"}, wantErr: true},
		{name: "bad shutdown timeout", env: map[string]string{"SHUTDOWN_TIMEOUT": "soon"}, wantErr: true},
		{name: "zero shutdown timeout", env: map[string]string{"SHUTDOWN_TIMEOUT": "0s"}, wantErr: true},
		{name: "negative shutdown timeout", env: map[string]string{"SHUTDOWN_TIMEOUT": "-1s"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadServerConfig(func(k string) string { return tt.env[k] })
			if tt.wantErr {
				if err == nil {
					t.Fatalf("LoadServerConfig() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadServerConfig() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadServerConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
