package tracectx_test

import (
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

// The spec's own example.
const example = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestParse(t *testing.T) {
	p, ok := tracectx.Parse(example)
	if !ok || p.TraceIDString() != "4bf92f3577b34da6a3ce929d0e0e4736" || p.Flags != 1 || p.String() != example {
		t.Fatalf("Parse(%q) = %+v, %v; want it back unchanged", example, p, ok)
	}
	for name, h := range map[string]string{
		"empty":            "",
		"another version":  "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"upper-case hex":   "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
		"not hex":          "00-4bf92f3577b34da6a3ce929d0e0e473z-00f067aa0ba902b7-01",
		"a zero trace ID":  "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"a zero span ID":   "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"too short":        "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-1",
		"wrong separators": "00_4bf92f3577b34da6a3ce929d0e0e4736_00f067aa0ba902b7_01",
		"a trailing extra": example + "-",
		"a log injection":  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-0\n",
	} {
		if _, ok := tracectx.Parse(h); ok {
			t.Errorf("%s: Parse(%q) accepted it", name, h)
		}
	}
}

// TestChild checks a call's context stays in the trace, with the flags, and
// gets a span of its own.
func TestChild(t *testing.T) {
	p, _ := tracectx.Parse(example)
	c := p.Child()
	if c.TraceID != p.TraceID || c.Flags != p.Flags || c.SpanID == p.SpanID {
		t.Errorf("Child = %s, of %s: want the same trace and flags, a new span", c, p)
	}
	if _, ok := tracectx.Parse(c.String()); !ok {
		t.Errorf("Child's header %q doesn't parse", c)
	}
}

func TestNew(t *testing.T) {
	a, b := tracectx.New(), tracectx.New()
	if a.TraceID == b.TraceID || a.Flags != 0 || !a.IsRoot() {
		t.Errorf("New = %s, %s: want distinct, unsampled traces, with no parent span", a, b)
	}
	// Its calls each get a span of their own.
	if c := a.Child(); c.IsRoot() || c.TraceID != a.TraceID {
		t.Errorf("New's Child = %s, want a span in its trace", c)
	} else if _, ok := tracectx.Parse(c.String()); !ok || !strings.HasPrefix(c.String(), "00-") {
		t.Errorf("New's Child's header %q doesn't parse", c)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(example)
	f.Add("00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-00")
	f.Fuzz(func(t *testing.T, h string) {
		p, ok := tracectx.Parse(h)
		if ok && p.String() != h {
			t.Errorf("Parse(%q) accepted it, but writes it back as %q", h, p.String())
		}
	})
}
