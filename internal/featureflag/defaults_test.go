package featureflag_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/featureflag"
)

func TestParseDefaults(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    featureflag.Defaults
		wantErr bool
	}{
		{
			name: "default variants",
			in: `{"flags":{
				"a":{"state":"ENABLED","variants":{"on":true,"off":false},"defaultVariant":"on"},
				"b":{"variants":{"on":true,"off":false},"defaultVariant":"off","targeting":{"if":[true,"on"]}}
			}}`,
			want: featureflag.Defaults{"a": true, "b": false},
		},
		{name: "no flags", in: `{"flags":{}}`, want: featureflag.Defaults{}},
		{name: "unknown default variant", in: `{"flags":{"a":{"variants":{"on":true},"defaultVariant":"nope"}}}`, wantErr: true},
		{name: "missing default variant", in: `{"flags":{"a":{"variants":{"on":true}}}}`, wantErr: true},
		{name: "non-boolean flag", in: `{"flags":{"a":{"variants":{"x":"red"},"defaultVariant":"x"}}}`, wantErr: true},
		{name: "malformed", in: `{"flags":`, wantErr: true},
		{name: "trailing data", in: `{"flags":{}} {}`, wantErr: true},
		{name: "empty", in: ``, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := featureflag.ParseDefaults([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatal("ParseDefaults() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDefaults() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Run("embedded flagd/flags.json", func(t *testing.T) {
		got, err := featureflag.LoadDefaults("")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Error("embedded flag definitions are empty")
		}
	})
	t.Run("file overrides embedded", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "flags.json")
		in := `{"flags":{"only-flag":{"variants":{"on":true},"defaultVariant":"on"}}}`
		if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := featureflag.LoadDefaults(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || !got["only-flag"] {
			t.Errorf("got %v, want only-flag=true", got)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, err := featureflag.LoadDefaults(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Error("LoadDefaults() error = nil, want error")
		}
	})
}

func TestRequire(t *testing.T) {
	d := featureflag.Defaults{"a": true}
	if err := d.Require("a"); err != nil {
		t.Errorf("Require(a) = %v, want nil", err)
	}
	if err := d.Require("a", "b", "c"); err == nil {
		t.Error("Require(a, b, c) = nil, want error naming b and c")
	}
}

func TestDeclare(t *testing.T) {
	if got := featureflag.Declare("declared-in-test"); got != "declared-in-test" {
		t.Errorf("Declare() = %q, want the key back", got)
	}
	found := false
	for _, k := range featureflag.Declared() {
		found = found || k == "declared-in-test"
	}
	if !found {
		t.Error("Declared() doesn't include the declared key")
	}
}
