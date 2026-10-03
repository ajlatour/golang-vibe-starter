package httpx

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbe(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(Healthz))
	defer healthy.Close()
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthy.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	_, healthyPort, _ := net.SplitHostPort(healthy.Listener.Addr().String())

	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{name: "healthy", addr: healthy.Listener.Addr().String()},
		{name: "wildcard listen address", addr: ":" + healthyPort},
		{name: "unhealthy", addr: unhealthy.Listener.Addr().String(), wantErr: true},
		{name: "nothing listening", addr: closed.Listener.Addr().String(), wantErr: true},
		{name: "bad address", addr: "no-port", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Probe(t.Context(), tt.addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("Probe(%q) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
		})
	}
}
