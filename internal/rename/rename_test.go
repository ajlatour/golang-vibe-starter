package rename_test

import (
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/rename"
)

func TestRename(t *testing.T) {
	order := rename.Renamer{From: "item", FromPlural: "items", To: "order", ToPlural: "orders"}
	category := rename.Renamer{From: "item", FromPlural: "items", To: "category", ToPlural: "categories"}
	for _, tt := range []struct {
		r        rename.Renamer
		in, want string
	}{
		{order, "package item", "package order"},
		{order, `"github.com/x/y/internal/item/itemtest"`, `"github.com/x/y/internal/order/ordertest"`},
		{order, "func compareItems(a, b item.Item) int", "func compareOrders(a, b order.Order) int"},
		{order, "ErrInvalidItem, MaxItems, upstreamItem, ITEM", "ErrInvalidOrder, MaxOrders, upstreamOrder, ORDER"},
		{order, "GET /items/{id}, items-list-total, new-item", "GET /orders/{id}, orders-list-total, new-order"},
		{order, "Items created in the same tick", "Orders created in the same tick"},
		{order, "Feature: Items\n", "Feature: Orders\n"},

		// Articles follow the new word.
		{order, "an item, An item, a widget", "an order, An order, a widget"},
		{category, "an item, a gadget", "a category, a gadget"},
		{category, "Getting an item's items", "Getting a category's categories"},

		// Not the resource's names: inside a word, the list's JSON key and
		// the field holding it.
		{order, "`json:\"description,omitempty\"`", "`json:\"description,omitempty\"`"},
		{order, `{"items": [], "items.0.id"}`, `{"items": [], "items.0.id"}`},
		{order, "`json:\"items\"` {\\\"items\\\":[ body.items[].name, the JSON array \"items\" has 3", "`json:\"items\"` {\\\"items\\\":[ body.items[].name, the JSON array \"items\" has 3"},
		{order, `"required": ["items"]`, `"required": ["items"]`},
		// A quoted plural that isn't the key is the resource's.
		{order, `fromPlural = "items"`, `fromPlural = "orders"`},
		{order, "l.Items, Items: x, Items []T", "l.Items, Items: x, Items []T"},
	} {
		if got := tt.r.Text(tt.in); got != tt.want {
			t.Errorf("%s: text(%q) = %q, want %q", tt.r.To, tt.in, got, tt.want)
		}
	}
}

// TestKeep checks a Renamer leaves what it keeps, such as a module path that
// names the resource.
func TestKeep(t *testing.T) {
	r := rename.Renamer{From: "order", FromPlural: "orders", To: "category", ToPlural: "categories", Keep: []string{"github.com/acme/orders"}}
	in := `import "github.com/acme/orders/internal/order" // orders`
	if got, want := r.Text(in), `import "github.com/acme/orders/internal/category" // categories`; got != want {
		t.Errorf("Text(%q) = %q, want %q", in, got, want)
	}
	if got := rename.Module("module github.com/acme/orders\n\ngo 1.27\n"); got != "github.com/acme/orders" {
		t.Errorf("Module = %q", got)
	}
}

// TestIrregular renames a resource whose plural isn't its name with an s,
// both ways.
func TestIrregular(t *testing.T) {
	r := rename.Renamer{From: "category", FromPlural: "categories", To: "item", ToPlural: "items"}
	in := "a category, Categories, GET /categories/{id}, categorytest, CATEGORY"
	if got, want := r.Text(in), "an item, Items, GET /items/{id}, itemtest, ITEM"; got != want {
		t.Errorf("Text(%q) = %q, want %q", in, got, want)
	}
}
