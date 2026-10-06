package item_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

func TestParseID(t *testing.T) {
	for _, s := range []string{"widget", "a", "new-item", "x_1", "3f2504e0-4f89-11d3-9a0c-0305e82c3301", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", strings.Repeat("a", item.MaxIDLen)} {
		id, err := item.ParseID(s)
		if err != nil || id.String() != s || id.IsZero() {
			t.Errorf("ParseID(%q) = %q, %v; want it back", s, id, err)
		}
	}
	for _, s := range []string{"", ".", "..", "...", "a.b", "../x", "a/b", "%2E%2E", "a%2Fb", "a b", "a\nb", "x?q", "x#f", "é", strings.Repeat("a", item.MaxIDLen+1)} {
		if id, err := item.ParseID(s); !errors.Is(err, item.ErrInvalidID) || !id.IsZero() {
			t.Errorf("ParseID(%q) = %q, %v; want the zero ID and ErrInvalidID", s, id, err)
		}
	}
}

// TestNewDraft checks the model's own rules: a client's name and
// description become a Draft only if they keep them, and the message for
// each broken rule is the one the API sends.
func TestNewDraft(t *testing.T) {
	d, err := item.NewDraft("  widget  ", "  as given  ")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name().String() != "widget" || d.Description().String() != "  as given  " {
		t.Errorf("draft = %q, %q; want the name trimmed and the description as given", d.Name(), d.Description())
	}
	if _, err := item.NewDraft(strings.Repeat("é", item.MaxNameLen), strings.Repeat("é", item.MaxDescriptionLen)); err != nil {
		t.Errorf("a draft at both limits, counted in characters: %v", err)
	}

	for _, tt := range []struct{ name, description, field, msg string }{
		{"", "", "name", "name is required"},
		{" \t\n", "", "name", "name is required"},
		{strings.Repeat("a", item.MaxNameLen+1), "", "name", "name must be at most 200 characters"},
		{"x", strings.Repeat("a", item.MaxDescriptionLen+1), "description", "description must be at most 2000 characters"},
		// Both broken: the name's is the one reported.
		{"", strings.Repeat("a", item.MaxDescriptionLen+1), "name", "name is required"},
	} {
		_, err := item.NewDraft(tt.name, tt.description)
		var ve *item.ValidationError
		if !errors.As(err, &ve) || ve.Field() != tt.field || err.Error() != tt.msg {
			t.Errorf("NewDraft(%.20q, %.20q) error = %v; want a *ValidationError on %s, %q", tt.name, tt.description, err, tt.field, tt.msg)
		}
	}
}

// TestRestore checks a store's item comes back as the store has it: the
// rules apply to what clients send, not to what's kept.
func TestRestore(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	long := strings.Repeat("a", item.MaxNameLen+1)
	it := item.Restore("legacy.id", long, "", at, at.Add(time.Hour))
	if it.ID.String() != "legacy.id" || it.Name.String() != long || !it.UpdatedAt.Equal(at.Add(time.Hour)) {
		t.Errorf("Restore = %+v, want the values as given", it)
	}
}

// TestParse checks an item from outside the service is held to the model's
// rules, and that the error says which without quoting the value, which
// came from outside.
func TestParse(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if it, err := item.Parse("widget", "Widget", "a widget", at, at); err != nil || it.ID.String() != "widget" {
		t.Fatalf("a valid item: %+v, %v", it, err)
	}
	for name, tt := range map[string]struct {
		id, name, desc string
		created        time.Time
	}{
		"an ID no item can have":   {"../admin", "Widget", "", at},
		"no name":                  {"widget", "", "", at},
		"an untrimmed name":        {"widget", " Widget ", "", at},
		"a name over the limit":    {"widget", strings.Repeat("n", item.MaxNameLen+1), "", at},
		"a description over limit": {"widget", "Widget", strings.Repeat("d", item.MaxDescriptionLen+1), at},
		"no timestamps":            {"widget", "Widget", "", time.Time{}},
	} {
		_, err := item.Parse(tt.id, tt.name, tt.desc, tt.created, tt.created)
		if !errors.Is(err, item.ErrInvalidItem) {
			t.Errorf("%s: %v, want ErrInvalidItem", name, err)
			continue
		}
		if strings.Contains(err.Error(), "../admin") || strings.Contains(err.Error(), "nnnn") {
			t.Errorf("%s: the error quotes the value: %v", name, err)
		}
	}
}
