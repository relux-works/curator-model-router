package recommend

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

// ValueProvenance belongs to an individual scalar, independent of other fields.
type ValueProvenance struct {
	Kind   string `json:"kind" toml:"kind"`
	Source string `json:"source" toml:"source"`
	AsOf   string `json:"as_of" toml:"as_of"`
}
type OverlayNumber struct {
	Value float64 `json:"value"`
	ValueProvenance
}
type OverlayTokens struct {
	Value int64 `json:"value"`
	ValueProvenance
}
type OverlayCost struct {
	USDPerTask    *OverlayNumber `json:"usd_per_task,omitempty"`
	TokensPerTask *OverlayTokens `json:"tokens_per_task,omitempty"`
}
type OverlayRow struct {
	Quality Quality     `json:"quality,omitempty"`
	Cost    OverlayCost `json:"cost,omitempty"`
}

// CatalogOverlay is keyed by exact runtime, model and effort. It never adds rows
// or changes admission, billing, identity or constraints. Omission preserves a
// field; explicit zero replaces it. Nulls and empty patches are refused.
type CatalogOverlay struct {
	SchemaVersion string                                      `json:"schema_version"`
	Rows          map[string]map[string]map[string]OverlayRow `json:"rows"`
}

// LoadOverlay consumes caller-supplied bytes only, with no filesystem discovery.
func LoadOverlay(raw []byte) (CatalogOverlay, error) {
	var o CatalogOverlay
	if err := strict(raw, &o, InvalidOverlay); err != nil {
		return o, err
	}
	return o, o.Validate()
}
func (o CatalogOverlay) Validate() error {
	if o.SchemaVersion != OverlayVersion || len(o.Rows) == 0 {
		return refuse(InvalidOverlay, "overlay-v1 and nonempty rows are required")
	}
	for rt, models := range o.Rows {
		if strings.TrimSpace(rt) == "" || len(models) == 0 {
			return refuse(InvalidOverlay, "empty runtime or models")
		}
		for model, efforts := range models {
			if strings.TrimSpace(model) == "" || len(efforts) == 0 {
				return refuse(InvalidOverlay, "empty model or efforts")
			}
			for effort, row := range efforts {
				if strings.TrimSpace(effort) == "" {
					return refuse(InvalidOverlay, "empty effort")
				}
				if row.Quality == (Quality{}) && row.Cost == (OverlayCost{}) {
					return refuse(InvalidOverlay, "empty patch")
				}
				// Reuse catalog quality validation, including measured-review uncertainty.
				c := Catalog{SchemaVersion: CatalogVersion, Rows: []CatalogRow{{Candidate: Candidate{Runtime: rt, Model: model, Effort: effort}, Family: "overlay", Billing: "subscription", Quality: row.Quality, Cost: Cost{Kind: "estimate", Source: "overlay validation", AsOf: "2026-10-07"}}}}
				if err := c.Validate(); err != nil {
					return refuse(InvalidOverlay, err.Error())
				}
				if v := row.Cost.USDPerTask; v != nil && (!finite(v.Value) || v.Value < 0 || !validProvenance(v.ValueProvenance)) {
					return refuse(InvalidOverlay, "invalid USD value or provenance")
				}
				if v := row.Cost.TokensPerTask; v != nil && (v.Value < 0 || !validProvenance(v.ValueProvenance)) {
					return refuse(InvalidOverlay, "invalid token value or provenance")
				}
			}
		}
	}
	if _, err := canonical.Marshal(o); err != nil {
		return refuse(InvalidOverlay, err.Error())
	}
	return nil
}
func validProvenance(p ValueProvenance) bool { return provenance(p.Kind, p.Source, p.AsOf) }

// Digest binds semantic canonical JSON, independent of whitespace and key order.
func (o CatalogOverlay) Digest() (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(o)
}

// MergeCatalog validates both inputs and returns a detached, sorted catalog.
// Each supplied scalar replaces its base field and its own provenance; unknown
// configurations are refused, even if not admitted for the current task.
func MergeCatalog(base Catalog, overlay CatalogOverlay) (Catalog, error) {
	return mergeCatalog(base, overlay, false)
}
func mergeCatalog(base Catalog, overlay CatalogOverlay, legacyReplay bool) (Catalog, error) {
	if err := base.Validate(); err != nil {
		return Catalog{}, err
	}
	if err := overlay.Validate(); err != nil {
		return Catalog{}, err
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return Catalog{}, err
	}
	var out Catalog
	if err = json.Unmarshal(raw, &out); err != nil {
		return Catalog{}, err
	}
	patches := map[string]OverlayRow{}
	for rt, models := range overlay.Rows {
		for model, efforts := range models {
			for effort, row := range efforts {
				patches[(Candidate{Runtime: rt, Model: model, Effort: effort}).Key()] = row
			}
		}
	}
	known := map[string]bool{}
	for _, r := range out.Rows {
		known[r.Key()] = true
	}
	keys := make([]string, 0, len(patches))
	for key := range patches {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if !known[key] {
			return Catalog{}, refuse(InvalidOverlay, "unknown configuration "+key)
		}
	}
	for _, r := range out.Rows {
		if r.Billing == "local" && !legacyReplay {
			if _, ok := patches[r.Key()]; ok {
				return Catalog{}, refuse(InvalidOverlay, "overlay-v1 cannot patch local quality or cost; use local-capability-v1")
			}
		}
	}
	for i := range out.Rows {
		r := &out.Rows[i]
		patch, ok := patches[r.Key()]
		if !ok {
			continue
		}
		if patch.Quality.Overall != nil {
			r.Quality.Overall = patch.Quality.Overall
		}
		if patch.Quality.Coding != nil {
			r.Quality.Coding = patch.Quality.Coding
		}
		if patch.Quality.Review != nil {
			r.Quality.Review = patch.Quality.Review
		}
		if v := patch.Cost.USDPerTask; v != nil {
			r.Cost.USDPerTask = &v.Value
			r.Cost.USDProvenance = &v.ValueProvenance
		}
		if v := patch.Cost.TokensPerTask; v != nil {
			r.Cost.TokensPerTask = &v.Value
			r.Cost.TokensProvenance = &v.ValueProvenance
		}
	}
	// Detach the overlay pointers too.
	raw, err = json.Marshal(out)
	if err != nil {
		return Catalog{}, err
	}
	out = Catalog{}
	if err = json.Unmarshal(raw, &out); err != nil {
		return Catalog{}, err
	}
	out = normalizeCatalog(out)
	return out, out.Validate()
}
