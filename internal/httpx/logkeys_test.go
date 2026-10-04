package httpx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogKeysAreConstants fails if any non-test code passes a string literal
// as a log field name, to slog.String and friends or to a logger's
// Info/Warn/Error/Debug/Log/With methods. Field names are declared once, as
// LogKey constants in log.go, so a single query finds every line that carries
// a given value and a rename happens in one place.
func TestLogKeysAreConstants(t *testing.T) {
	// Where the message sits in each logger method's arguments; the
	// key/value pairs follow it. With has no message.
	msgIndex := map[string]int{
		"Info": 0, "Warn": 0, "Error": 0, "Debug": 0,
		"InfoContext": 1, "WarnContext": 1, "ErrorContext": 1, "DebugContext": 1,
		"Log": 2, "With": -1,
	}
	// slog's Attr constructors take the key first.
	attrFuncs := map[string]bool{
		"String": true, "Int": true, "Int64": true, "Float64": true,
		"Bool": true, "Any": true, "Duration": true, "Time": true,
	}

	fset := token.NewFileSet()
	var violations []string
	report := func(n ast.Node, what string) {
		violations = append(violations, fset.Position(n.Pos()).String()+": "+what)
	}

	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name := sel.Sel.Name

				// slog.String("key", v) and friends.
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "slog" && attrFuncs[name] && len(call.Args) > 0 {
					if isStringLit(call.Args[0]) {
						report(call.Args[0], "slog."+name+" with a literal key "+litValue(call.Args[0]))
					}
					return true
				}

				// logger.Info("msg", "key", v, ...) and friends.
				if idx, ok := msgIndex[name]; ok && len(call.Args) > idx+1 {
					if idx >= 0 && !isStringLit(call.Args[idx]) {
						return true // not a logger call: the message isn't a literal
					}
					expectKey := true
					for _, arg := range call.Args[idx+1:] {
						switch {
						case expectKey && isStringLit(arg):
							report(arg, name+" with a literal key "+litValue(arg))
							expectKey = false
						case expectKey && isCall(arg):
							// An slog.Attr (e.g. DurationMS(d)) takes one slot.
						default:
							expectKey = !expectKey
						}
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

	for _, v := range violations {
		t.Errorf("%s: use a LogKey constant from internal/httpx/log.go", v)
	}
}

func isStringLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func isCall(e ast.Expr) bool {
	_, ok := e.(*ast.CallExpr)
	return ok
}

func litValue(e ast.Expr) string {
	return e.(*ast.BasicLit).Value
}
