package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

// catalogCodes returns the error codes internal/httpx/codes.go declares,
// the one list (httpx.Code).
func catalogCodes(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../../internal/httpx/codes.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			for _, v := range spec.(*ast.ValueSpec).Values {
				if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					c, _ := strconv.Unquote(lit.Value)
					codes = append(codes, c)
				}
			}
		}
	}
	if len(codes) < 10 {
		t.Fatalf("found %d codes in internal/httpx/codes.go; the file or this test have changed shape", len(codes))
	}
	slices.Sort(codes)
	return codes
}

// TestErrorCodesMatchTheSpec holds the Error schema's enum in
// docs/openapi.json to the code catalog, both ways: a code the service
// can send must be documented, and a documented one must exist. The
// scenario tests then check every response's code is in the enum.
func TestErrorCodesMatchTheSpec(t *testing.T) {
	s := loadSpec(t)
	props := s.lookupSchema(t, "Error")["properties"].(map[string]any)
	var enum []string
	for _, v := range props["code"].(map[string]any)["enum"].([]any) {
		enum = append(enum, v.(string))
	}
	slices.Sort(enum)
	if want := catalogCodes(t); !slices.Equal(enum, want) {
		t.Errorf("the Error schema's codes are %v; internal/httpx/codes.go has %v", enum, want)
	}
}

// TestErrorCodesMatchTheReadme holds the README's table of statuses and
// codes to the catalog: every code but client_closed (which no client
// reads: it has gone) is listed, and nothing else.
func TestErrorCodesMatchTheReadme(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, m := range regexp.MustCompile("(?m)^\\| \\d{3} \\| `([a-z_]+)` \\|").FindAllStringSubmatch(string(b), -1) {
		listed = append(listed, m[1])
	}
	slices.Sort(listed)
	want := slices.DeleteFunc(catalogCodes(t), func(c string) bool { return c == "client_closed" })
	if !slices.Equal(listed, want) {
		t.Errorf("the README lists codes %v; internal/httpx/codes.go has %v (client_closed aside)", listed, want)
	}
}
