package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestUnroutedRequestsAreJSON sends the whole service, over a real
// connection, the requests no route takes: each must get a JSON error with
// a code, and a "response sent" line. OPTIONS * above all, which the
// server would otherwise answer itself, unlogged.
func TestUnroutedRequestsAreJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	addr, logs, errc := startApp(t, ctx, map[string]string{"ADDR": "127.0.0.1:0", "UPSTREAM_URL": "http://mock", "API_AUTH": "off"})
	defer waitForShutdown(t, errc)
	defer cancel()

	for _, tc := range []struct {
		request string
		status  int
		code    string
	}{
		{"GET /nope HTTP/1.1", http.StatusNotFound, "not_found"},
		{"PATCH /items HTTP/1.1", http.StatusMethodNotAllowed, "method_not_allowed"},
		{"OPTIONS * HTTP/1.1", http.StatusBadRequest, "invalid_target"},
	} {
		logs.Reset()
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(conn, "%s\r\nHost: api\r\nConnection: close\r\n\r\n", tc.request)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.request, err)
		}
		b, _ := io.ReadAll(resp.Body)
		conn.Close()
		var body struct{ Code string }
		if resp.StatusCode != tc.status || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") || json.Unmarshal(b, &body) != nil || body.Code != tc.code {
			t.Errorf("%s: %d %q %s, want %d with code %s", tc.request, resp.StatusCode, resp.Header.Get("Content-Type"), b, tc.status, tc.code)
		}
		logged := false
		for _, r := range logs.Records(t) {
			logged = logged || (r["msg"] == "response sent" && r["status"] == float64(tc.status))
		}
		if !logged {
			t.Errorf("%s: no response sent line:\n%s", tc.request, logs)
		}
	}
}
