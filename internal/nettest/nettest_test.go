package nettest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
)

func TestDeadURL(t *testing.T) {
	_, err := (&http.Client{Timeout: time.Second}).Get(nettest.DeadURL())
	if err == nil {
		t.Fatal("Get(DeadURL) error = nil, want connection refused")
	}
}

func TestHang(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(nettest.Hang))
	defer srv.Close()
	_, err := (&http.Client{Timeout: 50 * time.Millisecond}).Get(srv.URL)
	if err == nil {
		t.Fatal("Get error = nil, want a timeout")
	}
}

// TestHangWithRequestBody checks the case that used to hang: a POST with a
// body, whose client times out. Server.Close must still return.
func TestHangWithRequestBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(nettest.Hang))
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer srv.Close()
		_, err := (&http.Client{Timeout: 50 * time.Millisecond}).Post(srv.URL, "application/json", strings.NewReader(`{"context":{}}`))
		if err == nil {
			t.Error("Post error = nil, want a timeout")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Server.Close did not return: Hang is stuck on a POST body")
	}
}

func TestDrop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(nettest.Drop))
	defer srv.Close()
	_, err := (&http.Client{Timeout: time.Second}).Get(srv.URL)
	if err == nil {
		t.Fatal("Get error = nil, want the connection to be dropped")
	}
}

func TestStatus(t *testing.T) {
	srv := httptest.NewServer(nettest.Status(http.StatusServiceUnavailable, "down"))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "down" {
		t.Errorf("got %d %q, want 503 down", resp.StatusCode, body)
	}

	empty := httptest.NewServer(nettest.Status(http.StatusBadGateway, ""))
	defer empty.Close()
	resp, err = http.Get(empty.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}
