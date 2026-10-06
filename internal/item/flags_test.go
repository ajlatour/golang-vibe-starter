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

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

// TestFlagKeysListsEveryFlag checks every flag constant in the package (a
// Flag... string constant) is in FlagKeys, which the service checks against
// the flag definitions at startup: a flag added but not listed would escape
// that check, and fall back to false if flagd didn't know it.
func TestFlagKeysListsEveryFlag(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Flag") || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					key, _ := strconv.Unquote(lit.Value)
					found++
					if !slices.Contains(item.FlagKeys(), key) {
						t.Errorf("%s (%q) isn't in FlagKeys: the startup check won't see it", name.Name, key)
					}
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("found no Flag constants: the test is looking in the wrong place")
	}
}
