package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpmetrics"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// BenchmarkAPI measures a request through everything the service does with
// it: the middleware, the header guard, the handler, the upstream call over
// real HTTP (to an in-process upstream, so the network is loopback and the
// upstream nearly free), and the JSON both ways, with logging on at INFO into
// io.Discard and the request metrics recorded, as in production. It's the in-process counterpart of the k6 load tests: what one
// request costs, without Docker or WireMock in the numbers.
func BenchmarkAPI(b *testing.B) {
	upstreamStore := itemtest.NewMemStore()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"widget", "gadget", "gizmo"} {
		upstreamStore.Seed(item.Restore(id, strings.ToUpper(id[:1])+id[1:], "An item.", at, at))
	}
	upstreamMux := http.NewServeMux()
	item.RegisterRoutes(upstreamMux, slog.New(slog.DiscardHandler), upstreamStore)
	upstream := httptest.NewServer(upstreamMux)
	b.Cleanup(upstream.Close)

	logger := logx.New(io.Discard, slog.LevelInfo, "json", "api", "bench")
	store := httpstore.New(upstream.URL, &http.Client{Timeout: 5 * time.Second}, httpstore.WithLogger(logger))
	// ClientIP and the request deadline as in production, where
	// LOG_CLIENT_IP is on and REQUEST_TIMEOUT 10s by default.
	api := newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: noAuth, Metrics: httpmetrics.New(), ClientIP: true, RequestTimeout: 10 * time.Second}, store)

	for _, tt := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"GET /items", http.MethodGet, "/items", "", http.StatusOK},
		{"GET /items/{id}", http.MethodGet, "/items/widget", "", http.StatusOK},
		{"POST /items", http.MethodPost, "/items", `{"name":"Bench","description":"Created by a benchmark."}`, http.StatusCreated},
		{"PUT /items/{id}", http.MethodPut, "/items/gadget", `{"name":"Gadget","description":"Replaced."}`, http.StatusOK},
		{"GET /items/{id}, not found", http.MethodGet, "/items/nope", "", http.StatusNotFound},
		{"POST /items, invalid", http.MethodPost, "/items", `{"name":""}`, http.StatusUnprocessableEntity},
	} {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				var body io.Reader
				if tt.body != "" {
					body = strings.NewReader(tt.body)
				}
				req := httptest.NewRequest(tt.method, tt.path, body)
				if tt.body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				w := httptest.NewRecorder()
				api.ServeHTTP(w, req)
				if w.Code != tt.want {
					b.Fatalf("status %d, want %d: %s", w.Code, tt.want, w.Body)
				}
			}
		})
	}
}
