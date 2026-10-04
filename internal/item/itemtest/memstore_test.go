package itemtest_test

import (
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
)

// TestMemStore checks the test fake itself, since the httpstore tests rely on
// it as their upstream.
func TestMemStore(t *testing.T) {
	itemtest.StoreContract(t, func(t *testing.T) item.Store { return itemtest.NewMemStore() })
}
