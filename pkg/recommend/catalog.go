package recommend

import "github.com/relux-works/curator-model-router/catalog"

// DefaultCatalog returns embedded catalog bytes for LoadCatalog. Admission
// remains the caller's responsibility, even when using the default catalog.
func DefaultCatalog() []byte { return catalog.Default() }
