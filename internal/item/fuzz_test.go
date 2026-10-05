package item_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// FuzzValidID checks what makes an ID safe to put in an upstream URL holds
// for every ID ValidID accepts: it needs no escaping, so no %2F can appear,
// and it can't be a dot segment a proxy would resolve.
func FuzzValidID(f *testing.F) {
	for _, seed := range []string{"widget", "new-item", ".", "..", "%2E%2E", "a/b", "a b", "é", "", strings.Repeat("a", 129)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		if !item.ValidID(id) {
			return
		}
		if url.PathEscape(id) != id {
			t.Errorf("ValidID(%q), but it needs escaping in a path: %q", id, url.PathEscape(id))
		}
		if id == "" || len(id) > item.MaxIDLen || strings.Trim(id, ".") == "" || strings.ContainsAny(id, "/.%") {
			t.Errorf("ValidID(%q): empty, too long, a dot segment, or with / . or %%", id)
		}
	})
}
