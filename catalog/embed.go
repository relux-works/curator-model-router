// Package catalog embeds the versioned default configuration catalog.
package catalog

import _ "embed"

//go:embed catalog.json
var defaultJSON []byte

// Default returns an independent copy of the embedded catalog JSON.
func Default() []byte { return append([]byte(nil), defaultJSON...) }
