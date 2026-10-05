package httpstore_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpapi"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
)

// TestStoreContract runs the store contract against a Store talking to an
// in-process upstream (the item routes over a MemStore), so it needs no
// container. TestStoreLive covers the real WireMock upstream.
func TestStoreContract(t *testing.T) {
	itemtest.StoreContract(t, func(t *testing.T) item.Store {
		return httpstore.New(newUpstream(t).URL, &http.Client{})
	})
}

// newUpstream starts an httptest server serving the item routes from a fresh
// MemStore.
func newUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, slog.New(slog.DiscardHandler), itemtest.NewMemStore())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// sameItem compares the fields a store must round-trip.
func sameItem(a, b item.Item) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Description == b.Description &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}
