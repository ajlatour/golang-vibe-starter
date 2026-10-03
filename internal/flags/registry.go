package flags

import (
	"slices"
	"sync"
)

var (
	registryMu sync.Mutex
	registry   = map[string]struct{}{}
)

// Declare registers key as a flag the application reads and returns it.
// Packages call it in a package-level var, which declares the flag and names
// it in one place:
//
//	var FlagListTotal = flags.Declare("items-list-total")
//
// At startup the service checks that every declared flag exists in the flag
// definitions, so a flag read in code can't lack a fallback value.
func Declare(key string) string {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[key] = struct{}{}
	return key
}

// Declared returns the keys passed to Declare, sorted.
func Declared() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
