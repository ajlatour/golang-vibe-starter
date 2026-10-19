package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRename(t *testing.T) {
	order := renamer{to: "order", toPlural: "orders"}
	category := renamer{to: "category", toPlural: "categories"}
	for _, tt := range []struct {
		r        renamer
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
		{order, "l.Items, Items: x, Items []T", "l.Items, Items: x, Items []T"},
	} {
		if got := tt.r.text(tt.in); got != tt.want {
			t.Errorf("%s: text(%q) = %q, want %q", tt.r.to, tt.in, got, tt.want)
		}
	}
}

func TestCopyMembers(t *testing.T) {
	r := renamer{to: "order", toPlural: "orders"}
	in := "{\n  \"paths\": {\n    \"/items\": {\"get\": {\"summary\": \"{ a brace\"}},\n    \"/items/{id}\": {}\n  },\n  \"x\": {}\n}\n"
	got, err := copyMembers(in, r, []string{`"/items": {`, `"/items/{id}": {`})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"paths\": {\n    \"/items\": {\"get\": {\"summary\": \"{ a brace\"}},\n    \"/items/{id}\": {},\n    \"/orders\": {\"get\": {\"summary\": \"{ a brace\"}},\n    \"/orders/{id}\": {}\n  },\n  \"x\": {}\n}\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := copyMembers(in, r, []string{`"/items/{id}": {`, `"x": {`}); err == nil {
		t.Error("members not side by side: no error")
	}
	if _, err := copyMembers(in, r, []string{`"nope": {`}); err == nil {
		t.Error("a member that isn't there: no error")
	}
}

func TestCopyLines(t *testing.T) {
	r := renamer{to: "order", toPlural: "orders"}
	in := "| a |\n| `internal/item` | x |\n| `internal/item/memstore` | y |\n| b |\n"
	got, err := copyLines(in, r, rowRE)
	if err != nil {
		t.Fatal(err)
	}
	want := "| a |\n| `internal/item` | x |\n| `internal/item/memstore` | y |\n| `internal/order` | x |\n| `internal/order/memstore` | y |\n| b |\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestResourceRefuses(t *testing.T) {
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"no name":                {},
		"capitals":               {"-name", "Order"},
		"a dash":                 {"-name", "line-item"},
		"a plural that's itself": {"-name", "sheep", "-plural", "sheep"},
		"the example's name":     {"-name", "lineitem"},
	} {
		if err := run(args, dir, io.Discard); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestResourceOnACopy adds a resource to a copy of this repository, then
// builds, vets and tests every module of the result, so make resource can't
// quietly fall behind the code it copies. make resource-check runs it
// (TEMPLATE_CHECK=1), as CI's template job does.
func TestResourceOnACopy(t *testing.T) {
	if os.Getenv("TEMPLATE_CHECK") == "" {
		t.Skip("TEMPLATE_CHECK is not set; make resource-check runs this")
	}
	dir := copyRepo(t)
	var out strings.Builder
	if err := run([]string{"-name", "category", "-plural", "categories"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	t.Log(out.String())

	goCmd := func(module string, args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = filepath.Join(dir, module)
		// Not this test again, on the copy's own copy.
		cmd.Env = append(os.Environ(), "TEMPLATE_CHECK=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: go %s: %v\n%s", module, strings.Join(args, " "), err, out)
		}
	}
	goCmd(".", "vet", "./...")
	goCmd("integration", "vet", "./...")
	goCmd(".", "test", "-count=1", "./...")
}

// copyRepo copies the files under version control, as they are in the
// working tree, to a new git repository, and returns it.
func copyRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	list, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for f := range strings.SplitSeq(strings.TrimRight(string(list), "\x00"), "\x00") {
		info, err := os.Stat(filepath.Join(root, f))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, info.Mode().Perm()); err != nil { // scripts stay executable
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// TestRun adds a resource to a small repository shaped like the template:
// the files it copies, and each list it adds to.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"internal/item/item.go":              "// Package item is an item.\npackage item\n\ntype Item struct{ Items []int `json:\"items\"` }\n",
		"cmd/api/items.go":                   "package main\n\nvar itemsResource = 1\n",
		"cmd/api/items_test.go":              "package main\n",
		"wiremock/mappings/items-read.json":  `{"request": {"url": "/items"}}` + "\n",
		"integration/features/items.feature": "Feature: Items\n",
		"cmd/api/resources.go":               "package main\n\nvar resources = []resource{\n\titemsResource,\n}\n",
		"cmd/api/resources_test.go":          "package main\n\nvar testResources = []testResource{\n\titemsTest,\n}\n",
		"docs/openapi.json":                  "{\n  \"paths\": {\n    \"/items\": {},\n    \"/items/{id}\": {}\n  },\n  \"parameters\": {\n    \"ItemID\": {}\n  },\n  \"schemas\": {\n    \"Item\": {},\n    \"ItemInput\": {},\n    \"ItemList\": {}\n  }\n}\n",
		"flagd/flags.json":                   "{\n  \"flags\": {\n    \"items-list-total\": {}\n  }\n}\n",
		"integration/flagd/flags.json":       "{\n  \"flags\": {\n    \"items-list-total\": {}\n  }\n}\n",
		"docs/feature-flags.md":              "| `items-list-total` | boolean |\n",
		"perf/k6.js":                         "const mix = [\n  { weight: 40, route: 'GET /items', call: () => 1 },\n];\n",
		"docs/architecture.md":               "| `internal/item` | feature |\n",
		"docs/curl.md":                       "# Calling the API\n\n## Plain curl\n",
	}
	for path, content := range files {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	var out strings.Builder
	if err := run([]string{"-name", "order"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"internal/order/order.go":             "package order\n\ntype Order struct{ Items []int `json:\"items\"` }",
		"cmd/api/orders.go":                   "var ordersResource = 1",
		"wiremock/mappings/orders-read.json":  `"/orders"`,
		"integration/features/orders.feature": "Feature: Orders",
		"cmd/api/resources.go":                "\titemsResource,\n\tordersResource,\n}",
		"cmd/api/resources_test.go":           "\titemsTest,\n\tordersTest,\n}",
		"docs/openapi.json":                   "\"/items/{id}\": {},\n    \"/orders\": {},\n    \"/orders/{id}\": {}",
		"flagd/flags.json":                    `"orders-list-total": {}`,
		"docs/feature-flags.md":               "`orders-list-total`",
		"perf/k6.js":                          "route: 'GET /orders'",
		"docs/architecture.md":                "| `internal/order` | feature |",
		"docs/curl.md":                        "### Orders\n",
	} {
		b, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s:\n%s\nwant it to contain %q", path, b, want)
		}
	}
	if !strings.Contains(out.String(), "Added /orders") {
		t.Errorf("output: %s", out.String())
	}
	if err := run([]string{"-name", "order"}, dir, io.Discard); err == nil {
		t.Error("adding it twice: no error")
	}
}
