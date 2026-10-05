package item_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

func TestNewName(t *testing.T) {
	tests := []struct {
		raw, want string
		wantErr   string
	}{
		{raw: "widget", want: "widget"},
		{raw: "  padded  ", want: "padded"},
		{raw: strings.Repeat("é", item.MaxNameLen), want: strings.Repeat("é", item.MaxNameLen)}, // counted in characters
		{raw: "  " + strings.Repeat("a", item.MaxNameLen) + "  ", want: strings.Repeat("a", item.MaxNameLen)},
		{raw: "", wantErr: "name is required"},
		{raw: " \t\n ", wantErr: "name is required"},
		{raw: strings.Repeat("a", item.MaxNameLen+1), wantErr: "name must be at most 200 characters"},
	}
	for _, tt := range tests {
		name, err := item.NewName(tt.raw)
		if tt.wantErr != "" {
			var invalid *item.ValidationError
			if !errors.As(err, &invalid) || invalid.Error() != tt.wantErr || invalid.Field != "name" || !name.IsZero() {
				t.Errorf("NewName(%.20q) = %q, %v; want the zero Name and a name ValidationError %q", tt.raw, name, err, tt.wantErr)
			}
			continue
		}
		if err != nil || name.String() != tt.want || name.IsZero() {
			t.Errorf("NewName(%.20q) = %q, %v; want %.20q", tt.raw, name, err, tt.want)
		}
	}
}

func TestMustNewNamePanicsOnAnInvalidName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustNewName(\"\") didn't panic")
		}
	}()
	item.MustNewName("")
}

func TestCheckStorable(t *testing.T) {
	if err := item.CheckStorable(item.Item{Name: item.MustNewName("ok")}); err != nil {
		t.Errorf("CheckStorable(valid) = %v, want nil", err)
	}
	var invalid *item.ValidationError
	if err := item.CheckStorable(item.Item{Description: "no name"}); !errors.As(err, &invalid) {
		t.Errorf("CheckStorable(no name) = %v, want a ValidationError", err)
	}
}
