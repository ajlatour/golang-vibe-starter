package featureflag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ajlatour/golang-vibe-starter/flagd"
)

// Defaults maps each flag key to the value used when flagd can't supply one.
// It is derived from flag definitions in flagd's format: each flag's
// defaultVariant, which is also what flagd serves when no targeting rule
// matches. Every flag the application reads must be present (see Declare).
type Defaults map[string]bool

// LoadDefaults reads flag definitions from the flagd-format JSON file at
// path, or the copy of flagd/flags.json embedded in the binary when path is
// empty, and returns each flag's default value.
func LoadDefaults(path string) (Defaults, error) {
	data := flagd.Flags
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("flag definitions: %w", err)
		}
		data = b
	}
	return ParseDefaults(data)
}

// ParseDefaults decodes flagd-format flag definitions. Only boolean flags are
// supported: a flag whose default variant isn't a boolean is an error.
func ParseDefaults(data []byte) (Defaults, error) {
	var doc struct {
		Flags map[string]struct {
			Variants       map[string]any `json:"variants"`
			DefaultVariant string         `json:"defaultVariant"`
		} `json:"flags"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("flag definitions: %w", err)
	}
	if dec.More() {
		return nil, errors.New("flag definitions: unexpected data after the JSON object")
	}

	d := make(Defaults, len(doc.Flags))
	for key, f := range doc.Flags {
		v, ok := f.Variants[f.DefaultVariant]
		if !ok {
			return nil, fmt.Errorf("flag definitions: flag %q: defaultVariant %q is not one of its variants", key, f.DefaultVariant)
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("flag definitions: flag %q: default variant %q is %T, only boolean flags are supported", key, f.DefaultVariant, v)
		}
		d[key] = b
	}
	return d, nil
}

// Require returns an error naming every key that has no definition, so a flag
// the application reads can't be left without a fallback.
func (d Defaults) Require(keys ...string) error {
	var missing []string
	for _, k := range keys {
		if _, ok := d[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("flag definitions: no flag defined for %s", strings.Join(missing, ", "))
	}
	return nil
}

// Bool returns the configured default for key, or false if the key isn't in
// the file. Defaults is itself an evaluator, used when no flag service is
// configured.
func (d Defaults) Bool(_ context.Context, key string) bool {
	return d[key]
}
