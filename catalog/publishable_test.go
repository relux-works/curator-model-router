package catalog

import (
	"encoding/json"
	"testing"
)

// The embedded catalog ships only data that may be republished. Index values
// (overall, coding) come from operator-local overlays and must never be
// committed here; this guard fails if one is added by accident.
func TestEmbeddedCatalogCarriesNoIndexValues(t *testing.T) {
	var doc struct {
		Rows []struct {
			Runtime string                     `json:"runtime"`
			Model   string                     `json:"model"`
			Effort  string                     `json:"effort"`
			Quality map[string]json.RawMessage `json:"quality"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(Default(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) == 0 {
		t.Fatal("embedded catalog has no rows")
	}
	for _, r := range doc.Rows {
		for key := range r.Quality {
			if key != "review" {
				t.Errorf("%s/%s/%s: quality.%s belongs in an operator overlay, not the embedded catalog", r.Runtime, r.Model, r.Effort, key)
			}
		}
	}
}
