package item_test

import (
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

func TestValidID(t *testing.T) {
	for _, id := range []string{"widget", "a", "new-item", "x_1", "3f2504e0-4f89-11d3-9a0c-0305e82c3301", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", strings.Repeat("a", item.MaxIDLen)} {
		if !item.ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", ".", "..", "...", "a.b", "../x", "a/b", "%2E%2E", "a%2Fb", "a b", "a\nb", "x?q", "x#f", "é", strings.Repeat("a", item.MaxIDLen+1)} {
		if item.ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}
