package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

// codeRoutes returns every route pattern the non-test code registers on a
// ServeMux ("GET /items/{id}"), found by reading the source. It skips
// internal/profiling, whose routes are served on a listener of their own
// (PPROF_ADDR), not the API's.
func codeRoutes(t *testing.T) map[string]bool {
	t.Helper()
	routeRE := regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS) /`)
	fset := token.NewFileSet()
	routes := map[string]bool{}
	for _, root := range []string{"../../internal", "."} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && path == filepath.Join("..", "..", "internal", "profiling") {
				return filepath.SkipDir
			}
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
					return true
				}
				switch arg := call.Args[0].(type) {
				case *ast.BasicLit: // mux.Handle("GET /items", ...)
					if p, err := strconv.Unquote(arg.Value); err == nil && routeRE.MatchString(p) {
						routes[p] = true
					}
				case *ast.BinaryExpr: // mux.HandleFunc("GET "+httpx.HealthzPath, ...)
					left, lok := arg.X.(*ast.BasicLit)
					right, rok := arg.Y.(*ast.SelectorExpr)
					paths := map[string]string{"HealthzPath": httpx.HealthzPath, "ReadyzPath": httpx.ReadyzPath}
					if path, known := paths[right.Sel.Name]; lok && rok && known {
						method, _ := strconv.Unquote(left.Value)
						routes[method+path] = true
					} else if lok {
						t.Errorf("%s: can't read this route pattern; teach codeRoutes about it so the spec check covers it", fset.Position(arg.Pos()))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return routes
}

// TestOpenAPIRoutesMatchCode fails if a route the code serves isn't in the
// spec, or a route in the spec isn't served.
func TestOpenAPIRoutesMatchCode(t *testing.T) {
	s := loadSpec(t)
	documented := map[string]bool{}
	for _, o := range s.operations(t) {
		documented[o.Key()] = true
	}
	code := codeRoutes(t)
	if len(code) == 0 {
		t.Fatal("found no routes in the code; the scanner is broken")
	}
	for r := range code {
		if !documented[r] {
			t.Errorf("the code serves %q, which is not in docs/openapi.json: document it", r)
		}
	}
	for r := range documented {
		if !code[r] {
			t.Errorf("docs/openapi.json documents %q, which the code doesn't serve: remove it or implement it", r)
		}
	}
}
