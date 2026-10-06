package item_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// FuzzParseID checks what makes an ID safe to put in an upstream URL holds
// for every ID ParseID accepts: it needs no escaping, so no %2F can appear,
// and it can't be a dot segment a proxy would resolve.
func FuzzParseID(f *testing.F) {
	for _, seed := range []string{"widget", "new-item", ".", "..", "%2E%2E", "a/b", "a b", "é", "", strings.Repeat("a", 129)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		parsed, err := item.ParseID(s)
		if err != nil {
			return
		}
		id := parsed.String()
		if id != s {
			t.Errorf("ParseID(%q).String() = %q, want it unchanged", s, id)
		}
		if url.PathEscape(id) != id {
			t.Errorf("ParseID(%q), but it needs escaping in a path: %q", id, url.PathEscape(id))
		}
		if id == "" || len(id) > item.MaxIDLen || strings.Trim(id, ".") == "" || strings.ContainsAny(id, "/.%") {
			t.Errorf("ParseID(%q): empty, too long, a dot segment, or with / . or %%", id)
		}
	})
}
