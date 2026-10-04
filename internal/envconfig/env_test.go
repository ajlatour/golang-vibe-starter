package envconfig

import (
	"strconv"
	"strings"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestOptional(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    int
		wantErr string // substring; "" means no error
	}{
		{name: "unset uses the default", env: nil, want: 7},
		{name: "empty counts as unset", env: map[string]string{"N": ""}, want: 7},
		{name: "set is parsed", env: map[string]string{"N": "42"}, want: 42},
		{name: "bad value falls back and is reported", env: map[string]string{"N": "many"}, want: 7, wantErr: "N: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewEnv(envFrom(tt.env))
			if got := Optional(e, "N", 7, strconv.Atoi); got != tt.want {
				t.Errorf("Optional() = %d, want %d", got, tt.want)
			}
			err := e.Err()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("Err() = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("Err() = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestRequired(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		e := NewEnv(envFrom(map[string]string{"N": "5"}))
		if got := Required(e, "N", strconv.Atoi); got != 5 || e.Err() != nil {
			t.Errorf("Required() = %d, Err() = %v, want 5 and nil", got, e.Err())
		}
	})
	t.Run("unset is an error naming the variable", func(t *testing.T) {
		e := NewEnv(envFrom(nil))
		if got := Required(e, "N", strconv.Atoi); got != 0 {
			t.Errorf("Required() = %d, want the zero value", got)
		}
		if err := e.Err(); err == nil || err.Error() != "N: required" {
			t.Errorf("Err() = %v, want %q", err, "N: required")
		}
	})
	t.Run("unparseable is an error", func(t *testing.T) {
		e := NewEnv(envFrom(map[string]string{"N": "x"}))
		Required(e, "N", strconv.Atoi)
		if e.Err() == nil {
			t.Error("Err() = nil, want a parse error")
		}
	})
}

// TestEnvReportsEveryProblem checks that one failed start lists all the bad
// settings, each named, rather than only the first.
func TestEnvReportsEveryProblem(t *testing.T) {
	e := NewEnv(envFrom(map[string]string{"A": "x", "C": "y"}))
	Optional(e, "A", 0, strconv.Atoi)
	Required(e, "B", strconv.Atoi)
	Optional(e, "C", 0, strconv.Atoi)

	err := e.Err()
	if err == nil {
		t.Fatal("Err() = nil, want errors")
	}
	for _, name := range []string{"A: ", "B: required", "C: "} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("Err() = %q, want it to mention %q", err, name)
		}
	}
	if joined, ok := err.(interface{ Unwrap() []error }); !ok || len(joined.Unwrap()) != 3 {
		t.Errorf("Err() = %#v, want 3 joined errors", err)
	}
}

func TestEnvString(t *testing.T) {
	e := NewEnv(envFrom(map[string]string{"S": "value"}))
	if got := e.String("S"); got != "value" {
		t.Errorf("String(S) = %q, want value", got)
	}
	if got := e.String("MISSING"); got != "" {
		t.Errorf("String(MISSING) = %q, want empty", got)
	}
	if e.Err() != nil {
		t.Errorf("String recorded an error: %v", e.Err())
	}
}
