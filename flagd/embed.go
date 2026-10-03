// Package flagd embeds the flag definitions that the flagd sidecar serves, so
// the API can take its fallback values from the same file and the two can't
// drift apart.
package flagd

import _ "embed"

// Flags is flags.json: the flag definitions in flagd's format.
//
//go:embed flags.json
var Flags []byte
