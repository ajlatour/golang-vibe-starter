package main

import (
	"log/slog"
	"net/http"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/item/memstore"
)

// itemsResource is /items (internal/item).
var itemsResource = resource{
	flagKeys: item.FlagKeys(),
	build: func(logger *slog.Logger, cfg config, f flags) (routes, []httpx.Check) {
		store, checks := newItemStore(logger, cfg)
		return itemRoutes(store, item.WithFlags(f)), checks
	},
}

// itemRoutes is the /items routes, on store.
func itemRoutes(store item.Store, opts ...item.Option) routes {
	return func(mux *http.ServeMux, logger *slog.Logger) {
		item.RegisterRoutes(mux, logger, store, opts...)
	}
}

// newItemStore returns the item store, and what readiness checks of it: the
// upstream, over HTTP, with its own timeout, connection pool and retries,
// ready while it's reachable; or memory (STORE=memory), always ready.
func newItemStore(logger *slog.Logger, cfg config) (item.Store, []httpx.Check) {
	if cfg.Store == storeMemory {
		return memstore.New(), nil
	}
	s := httpstore.New(cfg.UpstreamURL.String(),
		&http.Client{Timeout: cfg.UpstreamTimeout, Transport: newTransport(cfg.Limits.MaxInFlight)},
		httpstore.WithLogger(logger), httpstore.WithRetries(cfg.UpstreamRetries))
	return s, []httpx.Check{{Name: "upstream", Fn: s.Reachable}}
}
