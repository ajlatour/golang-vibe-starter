package apikey_test

import (
	"strings"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
)

// FuzzParseKeys throws arbitrary API_KEYS values at the parser: it never
// panics, and what it accepts parses the same way again when written back.
func FuzzParseKeys(f *testing.F) {
	h := apikey.HashHex("k")
	for _, seed := range []string{
		"ci:write:" + h, " a:read:" + h + " ,, b:write:" + h + ",", "ci:admin:" + h, "ci:write", ":::", ",,,",
		"ci:write:" + strings.ToUpper(h), "ci:write:" + h + ",ci:read:" + h, "ci:write:gvs_plain",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		keys, err := apikey.ParseKeys(s)
		if err != nil {
			return
		}
		var entries []string
		for _, k := range keys {
			if k.Scope != apikey.Read && k.Scope != apikey.Write {
				t.Fatalf("parsed scope %v", k.Scope)
			}
			entries = append(entries, k.Name+":"+k.Scope.String()+":"+hexOf(k.Hash[:]))
		}
		again, err := apikey.ParseKeys(strings.Join(entries, ","))
		if err != nil || len(again) != len(keys) {
			t.Fatalf("%q parsed to %d keys, which written back parse to %d, %v", s, len(keys), len(again), err)
		}
		for i := range keys {
			if again[i] != keys[i] {
				t.Errorf("key %d changed on a round trip: %+v, then %+v", i, keys[i], again[i])
			}
		}
	})
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}
