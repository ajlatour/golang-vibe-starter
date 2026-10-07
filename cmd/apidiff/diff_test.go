package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseSpec is a small spec in the shape of docs/openapi.json: an item, a
// list with a query parameter, a create with a body, and an error schema
// with a code enum. Each test case changes a copy of it.
const baseSpec = `{
  "openapi": "3.1.0",
  "paths": {
    "/items": {
      "get": {
        "parameters": [{"name": "limit", "in": "query", "schema": {"type": "integer", "minimum": 1, "maximum": 200}}],
        "responses": {
          "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ItemList"}}}},
          "400": {"$ref": "#/components/responses/BadRequest"}
        }
      },
      "post": {
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ItemInput"}}}},
        "responses": {
          "201": {"description": "created", "headers": {"ETag": {"schema": {"type": "string"}}}, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Item"}}}}
        }
      }
    },
    "/items/{id}": {
      "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
      "delete": {"responses": {"204": {"description": "gone"}, "404": {"$ref": "#/components/responses/BadRequest"}}}
    }
  },
  "components": {
    "responses": {"BadRequest": {"description": "bad", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}}},
    "schemas": {
      "Item": {"type": "object", "required": ["id", "name"], "properties": {"id": {"type": "string"}, "name": {"type": "string"}, "description": {"type": "string"}}},
      "ItemList": {"type": "object", "required": ["items"], "properties": {"items": {"type": "array", "items": {"$ref": "#/components/schemas/Item"}}}},
      "ItemInput": {"type": "object", "additionalProperties": false, "required": ["name"], "properties": {"name": {"type": "string", "maxLength": 200}, "description": {"type": "string"}, "colour": {"type": "string", "enum": ["red", "blue"]}}},
      "Error": {"type": "object", "required": ["error", "code"], "properties": {"error": {"type": "string"}, "code": {"type": "string", "enum": ["invalid_body", "not_found"]}}}
    }
  }
}`

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// at walks doc by keys, for a test case to change what it finds there.
func at(doc map[string]any, keys ...string) map[string]any {
	m := doc
	for _, k := range keys {
		m = m[k].(map[string]any)
	}
	return m
}

func TestDiff(t *testing.T) {
	schemas := func(d map[string]any) map[string]any { return at(d, "components", "schemas") }
	for _, tc := range []struct {
		name   string
		change func(d map[string]any)
		kind   Kind
		where  string // the change's location
	}{
		// Breaking.
		{"operation removed", func(d map[string]any) { delete(at(d, "paths", "/items/{id}"), "delete") }, Breaking, "DELETE /items/{id}"},
		{"new required parameter", func(d map[string]any) {
			op := at(d, "paths", "/items", "get")
			op["parameters"] = append(op["parameters"].([]any), map[string]any{"name": "tenant", "in": "header", "required": true})
		}, Breaking, "GET /items: parameter header tenant"},
		{"parameter became required", func(d map[string]any) {
			at(d, "paths", "/items", "get")["parameters"].([]any)[0].(map[string]any)["required"] = true
		}, Breaking, "GET /items: parameter query limit"},
		{"parameter range tightened", func(d map[string]any) {
			at(d, "paths", "/items", "get")["parameters"].([]any)[0].(map[string]any)["schema"].(map[string]any)["maximum"] = 100.0
		}, Breaking, "GET /items: parameter query limit"},
		{"request field became required", func(d map[string]any) {
			at(schemas(d), "ItemInput")["required"] = []any{"name", "description"}
		}, Breaking, "POST /items: request body.description"},
		{"new required request field", func(d map[string]any) {
			in := at(schemas(d), "ItemInput")
			at(in, "properties")["tags"] = map[string]any{"type": "array"}
			in["required"] = []any{"name", "tags"}
		}, Breaking, "POST /items: request body.tags"},
		{"request field removed, unknown refused", func(d map[string]any) {
			delete(at(schemas(d), "ItemInput", "properties"), "description")
		}, Breaking, "POST /items: request body.description"},
		{"request limit tightened", func(d map[string]any) {
			at(schemas(d), "ItemInput", "properties", "name")["maxLength"] = 100.0
		}, Breaking, "POST /items: request body.name"},
		{"request pattern added", func(d map[string]any) {
			at(schemas(d), "ItemInput", "properties", "name")["pattern"] = "^[a-z]+$"
		}, Breaking, "POST /items: request body.name"},
		{"request value no longer accepted", func(d map[string]any) {
			at(schemas(d), "ItemInput", "properties", "colour")["enum"] = []any{"red"}
		}, Breaking, "POST /items: request body.colour"},
		{"request values newly limited", func(d map[string]any) {
			at(schemas(d), "ItemInput", "properties", "description")["enum"] = []any{"short", "long"}
		}, Breaking, "POST /items: request body.description"},
		{"success status removed", func(d map[string]any) {
			r := at(d, "paths", "/items/{id}", "delete", "responses")
			r["200"] = r["204"]
			delete(r, "204")
		}, Breaking, "DELETE /items/{id}: response 204"},
		{"response field removed", func(d map[string]any) {
			delete(at(schemas(d), "Item", "properties"), "name")
		}, Breaking, "GET /items: response 200: body.items[].name"},
		{"response field may be absent", func(d map[string]any) {
			at(schemas(d), "Item")["required"] = []any{"id"}
		}, Breaking, "POST /items: response 201: body.name"},
		{"response header removed", func(d map[string]any) {
			delete(at(d, "paths", "/items", "post", "responses", "201"), "headers")
		}, Breaking, "POST /items: response 201: header ETag"},
		{"type changed", func(d map[string]any) {
			at(schemas(d), "Item", "properties", "id")["type"] = "integer"
		}, Breaking, "POST /items: response 201: body.id"},

		// Additive.
		{"new operation", func(d map[string]any) {
			at(d, "paths", "/items/{id}")["get"] = map[string]any{"responses": map[string]any{}}
		}, Additive, "GET /items/{id}"},
		{"new optional parameter", func(d map[string]any) {
			op := at(d, "paths", "/items", "get")
			op["parameters"] = append(op["parameters"].([]any), map[string]any{"name": "cursor", "in": "query"})
		}, Additive, "GET /items: parameter query cursor"},
		{"new response field", func(d map[string]any) {
			at(schemas(d), "ItemList", "properties")["next_cursor"] = map[string]any{"type": "string"}
		}, Additive, "GET /items: response 200: body.next_cursor"},
		{"new error code", func(d map[string]any) {
			c := at(schemas(d), "Error", "properties", "code")
			c["enum"] = append(c["enum"].([]any), "rate_limited")
		}, Additive, "GET /items: response 400: body.code"},
		{"new status", func(d map[string]any) {
			at(d, "paths", "/items", "get", "responses")["429"] = map[string]any{"$ref": "#/components/responses/BadRequest"}
		}, Additive, "GET /items: response 429"},
		{"request limit loosened", func(d map[string]any) {
			at(schemas(d), "ItemInput", "properties", "name")["maxLength"] = 500.0
		}, Additive, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, changed := decode(t, baseSpec), decode(t, baseSpec)
			tc.change(changed)
			changes := Diff(old, changed)
			if tc.where == "" {
				// A change that isn't one a client can see.
				for _, c := range changes {
					if c.Kind == Breaking {
						t.Errorf("flagged as breaking: %+v", c)
					}
				}
				return
			}
			found := false
			for _, c := range changes {
				if c.Where == tc.where && c.Kind == tc.kind {
					found = true
				}
				if tc.kind == Additive && c.Kind == Breaking {
					t.Errorf("an additive change flagged as breaking: %+v", c)
				}
			}
			if !found {
				t.Errorf("no %v change at %q; got %+v", tc.kind, tc.where, changes)
			}
		})
	}
}

func TestNoChange(t *testing.T) {
	doc := decode(t, baseSpec)
	described := decode(t, baseSpec)
	at(described, "paths", "/items", "get", "responses", "200")["description"] = "reworded" // prose isn't contract
	if changes := Diff(doc, described); len(changes) != 0 {
		t.Errorf("changes = %+v, want none", changes)
	}
}

func TestBumps(t *testing.T) {
	breaking := []Change{{Kind: Breaking}}
	additive := []Change{{Kind: Additive}}
	for _, tc := range []struct {
		changes  []Change
		from     [3]int
		required string
	}{
		{nil, [3]int{0, 6, 0}, "patch"},
		{additive, [3]int{0, 6, 0}, "minor"},
		{breaking, [3]int{0, 6, 0}, "minor"}, // 0.x: a minor release may break
		{breaking, [3]int{1, 2, 0}, "major"},
		{additive, [3]int{1, 2, 0}, "minor"},
	} {
		if got := RequiredBump(tc.changes, tc.from); got != tc.required {
			t.Errorf("RequiredBump(%v, %v) = %s, want %s", tc.changes, tc.from, got, tc.required)
		}
	}
	for _, tc := range []struct {
		from, to string
		want     string
	}{
		{"v0.6.0", "v0.6.1", "patch"}, {"v0.6.0", "v0.7.0", "minor"}, {"v0.6.3", "v1.0.0", "major"},
		{"v0.6.0", "v0.6.0", ""}, {"v0.6.0", "v0.5.9", ""},
	} {
		f, _ := ParseVersion(tc.from)
		to, _ := ParseVersion(tc.to)
		if got := Bump(f, to); got != tc.want {
			t.Errorf("Bump(%s, %s) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
	if !Enough("major", "minor") || Enough("patch", "minor") || Enough("", "patch") {
		t.Error("Enough ranks bumps wrong")
	}
	for _, bad := range []string{"0.6.0", "v0.6", "v0.6.x", "v-1.0.0"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) accepted it", bad)
		}
	}
}

// TestChecks runs the command's two checks: a release's version bump, and
// a pull request's changelog entry for a breaking change.
func TestChecks(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := write("old.json", baseSpec)
	broken := decode(t, baseSpec)
	delete(at(broken, "paths", "/items/{id}"), "delete")
	b, _ := json.Marshal(broken)
	head := write("new.json", string(b))
	noEntry := write("CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n### Added\n\n- Something.\n\n## [0.6.0] - 2026-10-07\n\n- **Breaking**: an old one.\n")
	withEntry := write("CHANGELOG2.md", "# Changelog\n\n## Unreleased\n\n### Removed\n\n- **Breaking**: `DELETE /items/{id}` is gone.\n")
	changesDir := filepath.Join(dir, "changes")
	if err := os.Mkdir(changesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	emptyChanges := filepath.Join(dir, "no-changes")
	if err := os.Mkdir(emptyChanges, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(changesDir, "remove-delete.md"), []byte("### Removed\n\n- **Breaking**: `DELETE /items/{id}` is gone.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		args   []string
		failed bool
	}{
		{"patch release with a breaking change", []string{"-base", base, "-head", head, "-from", "v0.6.0", "-to", "v0.6.1"}, true},
		{"minor release with a breaking change, 0.x", []string{"-base", base, "-head", head, "-from", "v0.6.0", "-to", "v0.7.0"}, false},
		{"minor release with a breaking change, 1.x", []string{"-base", base, "-head", head, "-from", "v1.6.0", "-to", "v1.7.0"}, true},
		{"breaking, no changelog entry (an older release's doesn't count)", []string{"-base", base, "-head", head, "-changelog", noEntry}, true},
		{"breaking, with a changelog entry", []string{"-base", base, "-head", head, "-changelog", withEntry}, false},
		{"breaking, with an entry in a change file", []string{"-base", base, "-head", head, "-changelog", noEntry, "-changes", changesDir}, false},
		{"breaking, and no change file says so", []string{"-base", base, "-head", head, "-changelog", noEntry, "-changes", emptyChanges}, true},
		{"nothing changed", []string{"-base", base, "-head", base, "-changelog", noEntry, "-from", "v0.6.0", "-to", "v0.6.1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			err := run(tc.args, &out)
			var failed checkFailed
			if got := errors.As(err, &failed); got != tc.failed || (err != nil && !got) {
				t.Errorf("err = %v, want a failed check: %v\n%s", err, tc.failed, out.String())
			}
		})
	}
}
