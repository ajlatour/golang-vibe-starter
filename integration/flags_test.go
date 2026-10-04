package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Feature flags are switched by editing a file, never by touching a
// container: compose.integration.yaml mounts integration/flagd into flagd in
// place of the real definitions, and flagd reloads the file when it changes.
// The suite resets that copy from flagd/flags.json when it starts, so it
// always mirrors what the service ships with, and a run that was interrupted
// mid-scenario can't leave a flag switched for the next one.
const (
	realFlagsFile = "../flagd/flags.json" // the definitions the service ships with
	testFlagsFile = "flagd/flags.json"    // the tests' copy, mounted into flagd
)

// flagReloadTimeout bounds how long a step waits for flagd to pick up an
// edited file.
const flagReloadTimeout = 10 * time.Second

// resetFlags makes the tests' copy identical to the real definitions and
// waits until flagd serves every flag's default from it.
func resetFlags(s stack) error {
	shipped, err := os.ReadFile(realFlagsFile)
	if err != nil {
		return err
	}
	if current, err := os.ReadFile(testFlagsFile); err != nil || !bytes.Equal(current, shipped) {
		if err := writeFileAtomic(testFlagsFile, shipped); err != nil {
			return err
		}
	}
	defaults, err := flagDefaults(shipped)
	if err != nil {
		return err
	}
	for key, want := range defaults {
		if err := waitForFlag(s, key, want); err != nil {
			return err
		}
	}
	return nil
}

// setFlag switches a boolean flag in the tests' copy by changing its default
// variant, then waits until flagd serves the new value.
func setFlag(s stack, key string, on bool) error {
	b, err := os.ReadFile(testFlagsFile)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("%s: %w", testFlagsFile, err)
	}
	flags, _ := doc["flags"].(map[string]any)
	flag, ok := flags[key].(map[string]any)
	if !ok {
		return fmt.Errorf("flag %q is not defined in %s", key, realFlagsFile)
	}
	variants, _ := flag["variants"].(map[string]any)
	variant := ""
	for name, value := range variants {
		if value == on {
			variant = name
		}
	}
	if variant == "" {
		return fmt.Errorf("flag %q has no variant with the value %v", key, on)
	}
	flag["defaultVariant"] = variant

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(testFlagsFile, append(out, '\n')); err != nil {
		return err
	}
	return waitForFlag(s, key, on)
}

// flagDefaults returns each flag's default value from a flags file.
func flagDefaults(b []byte) (map[string]bool, error) {
	var doc struct {
		Flags map[string]struct {
			Variants       map[string]any `json:"variants"`
			DefaultVariant string         `json:"defaultVariant"`
		} `json:"flags"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for key, f := range doc.Flags {
		if v, ok := f.Variants[f.DefaultVariant].(bool); ok {
			out[key] = v
		}
	}
	return out, nil
}

// writeFileAtomic replaces path in one step, through a temporary file and a
// rename, so flagd never reads a half-written file.
func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".flags-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// waitForFlag polls flagd's OFREP endpoint until it serves want for key.
func waitForFlag(s stack, key string, want bool) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(flagReloadTimeout)
	var last string
	for time.Now().Before(deadline) {
		got, err := evaluateFlag(client, s, key)
		if err == nil && got == want {
			return nil
		}
		if err != nil {
			last = err.Error()
		} else {
			last = fmt.Sprintf("still %v", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("flagd at %s didn't serve %s=%v within %s (%s)", s.flagd, key, want, flagReloadTimeout, last)
}

func evaluateFlag(client *http.Client, s stack, key string) (bool, error) {
	resp, err := client.Post(s.flagd+"/ofrep/v1/evaluate/flags/"+url.PathEscape(key), "application/json",
		bytes.NewReader([]byte(`{"context":{}}`)))
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s: %s", resp.Status, body)
	}
	var out struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false, err
	}
	v, ok := out.Value.(bool)
	if !ok {
		return false, fmt.Errorf("value is %T, want bool", out.Value)
	}
	return v, nil
}
