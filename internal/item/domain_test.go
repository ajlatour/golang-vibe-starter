package item_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// allowedImports is everything the domain may import: the standard library's
// neutral building blocks. Nothing for HTTP, JSON, logging, configuration or
// any of this module's technical packages. Widen it only for something
// genuinely free of infrastructure.
var allowedImports = []string{"context", "errors", "fmt", "strings", "time", "unicode/utf8"}

// TestDomainIsPure keeps package item a pure domain model: it fails if any of
// its (non-test) files imports a package outside allowedImports, or gives a
// type serialization tags (such as json:"..."), which would tie the model to a
// wire format. Those belong in httpapi and httpstore, which translate.
func TestDomainIsPure(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !slices.Contains(allowedImports, p) {
				t.Errorf("%s imports %q: the domain must not depend on technical packages (allowed: %v)",
					fset.Position(imp.Pos()), p, allowedImports)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if field, ok := n.(*ast.Field); ok && field.Tag != nil {
				t.Errorf("%s: a field has the tag %s; the domain carries no serialization tags",
					fset.Position(field.Pos()), field.Tag.Value)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("found no domain files to check")
	}
}
