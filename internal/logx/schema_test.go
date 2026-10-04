package logx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

// These tests hold three descriptions of the log schema to each other, so
// they can't drift: the code (which messages it emits, with which field
// names), the contract table (logtest.Contract), and the README. Field names
// are declared once, as the Key constants in keys.go.

// logCall is one logging call found in non-test code.
type logCall struct {
	pos         token.Position
	name        string   // the method or slog function called
	msg         string   // the message, if the call passes a string literal
	literalKeys []string // field names passed as string literals (should be none)
}

// findLogCalls parses the non-test code of the module and returns every
// logging call: slog.String and friends, and a logger's Info, Warn, Error,
// Debug, their Context forms, Log, LogAttrs and With. It works from syntax
// alone, so a key or message passed through a variable isn't seen, which is
// the point of declaring them as constants and literals.
func findLogCalls(t *testing.T) []logCall {
	t.Helper()
	// Where the message sits in each logger method's arguments; the
	// key/value pairs follow it. With has no message.
	msgIndex := map[string]int{
		"Info": 0, "Warn": 0, "Error": 0, "Debug": 0,
		"InfoContext": 1, "WarnContext": 1, "ErrorContext": 1, "DebugContext": 1,
		"Log": 2, "LogAttrs": 2, "With": -1,
	}
	// slog's Attr constructors take the key first.
	attrFuncs := map[string]bool{
		"String": true, "Int": true, "Int64": true, "Float64": true,
		"Bool": true, "Any": true, "Duration": true, "Time": true,
	}

	fset := token.NewFileSet()
	var calls []logCall
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
						calls = append(calls, logCall{pos: fset.Position(call.Pos()), name: "slog." + name, literalKeys: []string{litValue(call.Args[0])}})
					}
					return true
				}

				// logger.Info("msg", "key", v, ...) and friends.
				idx, ok := msgIndex[name]
				// The message must be present (With has none, but needs at
				// least one argument); key/value pairs after it are optional.
				if !ok || len(call.Args) < max(idx+1, 1) {
					return true
				}
				lc := logCall{pos: fset.Position(call.Pos()), name: name}
				if idx >= 0 {
					if !isStringLit(call.Args[idx]) {
						return true // not a logger call: the message isn't a literal
					}
					lc.msg = litValue(call.Args[idx])
				}
				expectKey := true
				for _, arg := range call.Args[idx+1:] {
					switch {
					case expectKey && isStringLit(arg):
						lc.literalKeys = append(lc.literalKeys, litValue(arg))
						expectKey = false
					case expectKey && isCall(arg):
						// An slog.Attr (e.g. DurationMS(d)) takes one slot.
					default:
						expectKey = !expectKey
					}
				}
				calls = append(calls, lc)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return calls
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
	s, err := strconv.Unquote(e.(*ast.BasicLit).Value)
	if err != nil {
		return e.(*ast.BasicLit).Value
	}
	return s
}

// declaredKeys returns the values of the Key constants in keys.go.
func declaredKeys(t *testing.T) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "keys.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if strings.HasPrefix(name.Name, "Key") && i < len(vs.Values) && isStringLit(vs.Values[i]) {
					keys[litValue(vs.Values[i])] = true
				}
			}
		}
	}
	if len(keys) == 0 {
		t.Fatal("found no Key constants in keys.go")
	}
	return keys
}

// TestLogKeysAreConstants fails if any non-test code passes a string literal
// as a log field name. Field names are declared once, as Key constants in
// keys.go, so a single query finds every line that carries a given value and
// a rename happens in one place.
func TestLogKeysAreConstants(t *testing.T) {
	for _, c := range findLogCalls(t) {
		for _, k := range c.literalKeys {
			t.Errorf("%s: %s with a literal key %q: use a Key constant from internal/logx/keys.go", c.pos, c.name, k)
		}
	}
}

// TestEveryMessageIsInContract fails if the code can log a message that
// logtest.Contract doesn't list, or if the contract lists one the code never
// logs. Either way the schema and the code disagree.
func TestEveryMessageIsInContract(t *testing.T) {
	emitted := map[string]bool{}
	for _, c := range findLogCalls(t) {
		if c.msg == "" {
			continue
		}
		emitted[c.msg] = true
		if _, ok := logtest.Contract[c.msg]; !ok {
			t.Errorf("%s: %s logs %q, which is not in logtest.Contract: add it with its levels and fields", c.pos, c.name, c.msg)
		}
	}
	for msg := range logtest.Contract {
		if !emitted[msg] {
			t.Errorf("logtest.Contract lists %q, but no code logs it: remove the entry (or the message was renamed)", msg)
		}
	}
	for _, msg := range logtest.OutsideRequests {
		if _, ok := logtest.Contract[msg]; !ok {
			t.Errorf("logtest.OutsideRequests lists %q, which is not in logtest.Contract", msg)
		}
	}
}

// TestContractFieldsAreDeclared fails if the contract names a field that
// isn't a Key constant, for example because of a typo.
func TestContractFieldsAreDeclared(t *testing.T) {
	keys := declaredKeys(t)
	for msg, m := range logtest.Contract {
		for _, k := range m.Keys {
			if !keys[k] {
				t.Errorf("logtest.Contract[%q] requires field %q, which is not a Key constant", msg, k)
			}
		}
		for _, l := range m.Levels {
			if !slices.Contains([]string{"DEBUG", "INFO", "WARN", "ERROR"}, l) {
				t.Errorf("logtest.Contract[%q] has unknown level %q", msg, l)
			}
		}
	}
}

// loggingSection returns the README's Logging section.
func loggingSection(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "\n## Logging")
	if i < 0 {
		t.Fatal("README has no '## Logging' section")
	}
	s = s[i+1:]
	if j := strings.Index(s[3:], "\n## "); j >= 0 {
		s = s[:j+3]
	}
	return s
}

// tableRows returns the rows of the markdown table whose header row starts
// with header, each as its trimmed cells.
func tableRows(t *testing.T, section, header string) [][]string {
	t.Helper()
	var rows [][]string
	in := false
	for _, line := range strings.Split(section, "\n") {
		switch {
		case !in && strings.HasPrefix(line, header):
			in = true
		case in && strings.HasPrefix(line, "|"):
			cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			if !strings.HasPrefix(strings.ReplaceAll(cells[0], " ", ""), "---") {
				rows = append(rows, cells)
			}
		case in:
			return rows
		}
	}
	if !in {
		t.Fatalf("README Logging section has no table starting %q", header)
	}
	return rows
}

var backticked = regexp.MustCompile("`([^`]+)`")

func ticks(cell string) []string {
	var out []string
	for _, m := range backticked.FindAllStringSubmatch(cell, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestREADMEFieldTable fails if the README's field table and the Key
// constants disagree: every declared field must be documented, and every
// documented field must exist.
func TestREADMEFieldTable(t *testing.T) {
	keys := declaredKeys(t)
	builtin := map[string]bool{"time": true, "level": true, "msg": true} // added by slog
	documented := map[string]bool{}
	for _, row := range tableRows(t, loggingSection(t), "| Field") {
		for _, f := range ticks(row[0]) {
			documented[f] = true
			if !keys[f] && !builtin[f] {
				t.Errorf("README documents field %q, which is not a Key constant (or slog built-in)", f)
			}
		}
	}
	for k := range keys {
		if !documented[k] {
			t.Errorf("README's field table doesn't document %q (a Key constant)", k)
		}
	}
}

// TestREADMEMessagesTable fails if the README's Messages table and
// logtest.Contract disagree about which messages exist, the levels they may
// be logged at, or the fields they carry.
func TestREADMEMessagesTable(t *testing.T) {
	rows := tableRows(t, loggingSection(t), "| Message")
	documented := map[string]bool{}
	for _, row := range rows {
		if len(row) < 3 {
			t.Fatalf("Messages table row has %d cells, want at least 3 (Message, Level, Fields): %v", len(row), row)
		}
		names := ticks(row[0])
		if len(names) != 1 {
			t.Errorf("Messages table row %v: first cell must hold exactly one `message`", row)
			continue
		}
		msg := names[0]
		documented[msg] = true
		want, ok := logtest.Contract[msg]
		if !ok {
			t.Errorf("README documents message %q, which is not in logtest.Contract", msg)
			continue
		}
		for _, level := range want.Levels {
			if !strings.Contains(row[1], level) {
				t.Errorf("README says %q is logged at %q; the contract allows %v", msg, row[1], want.Levels)
				break
			}
		}
		for _, level := range []string{"DEBUG", "INFO", "WARN", "ERROR"} {
			if strings.Contains(row[1], level) && !slices.Contains(want.Levels, level) {
				t.Errorf("README says %q may be logged at %s; the contract allows only %v", msg, level, want.Levels)
			}
		}
		got := ticks(row[2])
		sort.Strings(got)
		wantKeys := slices.Clone(want.Keys)
		sort.Strings(wantKeys)
		if !slices.Equal(got, wantKeys) {
			t.Errorf("README lists fields %v for %q; the contract requires %v", got, msg, wantKeys)
		}
	}
	for msg := range logtest.Contract {
		if !documented[msg] {
			t.Errorf("README's Messages table doesn't document %q", msg)
		}
	}
}
