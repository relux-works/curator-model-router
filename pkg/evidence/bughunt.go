package evidence

import (
	"fmt"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/skill-agents-management/pkg/vendorplugin/benchdata"
)

const (
	ModuleVersion = "v0.5.40"
	// These are release-declared retrieval/import metadata, never clock readings.
	bugHuntReleaseAt      = "2026-10-01T00:00:00Z"
	bugHuntMappingVersion = "bughunt-model-map-v1"
)

// BugHuntExport is the data-only interchange shape for the module. Registry
// facts and all bench claims are exported together; no vendor plugin is loaded.
type BugHuntExport struct {
	SchemaVersion  string          `json:"schema_version"`
	ModuleVersion  string          `json:"module_version"`
	ImportedAt     string          `json:"imported_at"`
	RetrievedAt    string          `json:"retrieved_at"`
	MappingVersion string          `json:"mapping_version"`
	Models         []RegistryModel `json:"models"`
	Mapping        []ModelMapping  `json:"mapping"`
	Rows           []BugHuntRow    `json:"rows"`
}
type ModelMapping struct {
	ExternalName string `json:"external_name"`
	ModelID      string `json:"model_id"`
}
type BugHuntRow struct {
	Key           string  `json:"key"`
	ModelName     string  `json:"model_name"`
	Effort        string  `json:"effort,omitempty"`
	Kind          string  `json:"kind"`
	Value         float64 `json:"value"`
	Cost          *Cost   `json:"cost,omitempty"`
	OriginRef     string  `json:"origin_ref"`
	Denominator   *int64  `json:"denominator,omitempty"`
	SourceVersion string  `json:"source_version,omitempty"`
}

// PinnedExport constructs an independent export from the compiled data-only
// accessors. The identity mapping is explicit and versioned.
func PinnedExport() (BugHuntExport, error) {
	v := BugHuntExport{
		SchemaVersion: SchemaVersion, ModuleVersion: ModuleVersion,
		ImportedAt: bugHuntReleaseAt, RetrievedAt: bugHuntReleaseAt,
		MappingVersion: bugHuntMappingVersion,
		Models:         []RegistryModel{}, Mapping: []ModelMapping{}, Rows: []BugHuntRow{},
	}
	for _, fact := range benchdata.RegistryFacts() {
		v.Models = append(v.Models, RegistryModel{ModelID: fact.ModelID, Efforts: fact.Efforts})
		v.Mapping = append(v.Mapping, ModelMapping{ExternalName: fact.ModelID, ModelID: fact.ModelID})
	}
	for _, row := range benchdata.BugHuntRows() {
		var cost *Cost
		if row.Cost != nil {
			cost = &Cost{TokensIn: row.Cost.TokensIn, TokensOut: row.Cost.TokensOut, USD: row.Cost.USD, WallS: row.Cost.WallS}
		}
		v.Rows = append(v.Rows, BugHuntRow{
			Key: row.Key, ModelName: row.ModelID, Effort: row.Effort,
			Kind: row.Kind, Value: row.Value, Denominator: row.Denominator,
			Cost: cost, OriginRef: row.OriginRef, SourceVersion: row.SourceVersion,
		})
	}
	return v, v.Registry().Validate()
}
func (v BugHuntExport) Registry() Registry {
	return Registry{SchemaVersion, Identity{"skill-agents-management", v.ModuleVersion}, v.Models}
}
func PinnedRegistry() (Registry, error) {
	v, err := PinnedExport()
	if err != nil {
		return Registry{}, err
	}
	r := v.Registry()
	return r, r.Validate()
}
func BugHunt(raw []byte, importedAt string) (Import, Report, error) {
	var v BugHuntExport
	if err := Decode(raw, &v); err != nil {
		return Import{}, Report{}, err
	}
	return ImportBugHunt(v, importedAt)
}
func ImportBugHunt(v BugHuntExport, at string) (Import, Report, error) {
	if v.SchemaVersion != SchemaVersion || v.ModuleVersion != ModuleVersion || v.MappingVersion != bugHuntMappingVersion {
		return Import{}, Report{}, refuse("evidence_bughunt_version", "unsupported export or mapping version")
	}
	if err := canonical.CheckOrdered(v.Mapping, func(m ModelMapping) string { return m.ExternalName }); err != nil {
		return Import{}, Report{}, err
	}
	if err := canonical.CheckOrdered(v.Rows, func(r BugHuntRow) string { return r.Key }); err != nil {
		return Import{}, Report{}, err
	}
	if _, err := instant(v.ImportedAt); err != nil {
		return Import{}, Report{}, refuse("evidence_invalid_time", "export imported_at must be RFC3339")
	}
	if _, err := instant(v.RetrievedAt); err != nil {
		return Import{}, Report{}, refuse("evidence_invalid_time", "export retrieved_at must be RFC3339")
	}
	if at == "" {
		at = v.ImportedAt
	}
	r := v.Registry()
	if err := r.Validate(); err != nil {
		return Import{}, Report{}, err
	}
	doc := EmptyImport("bug-hunt", Identity{"bughunt", "2"}, at)
	doc.MeasuredBy = "third_party"
	doc.Benchmarks = []Benchmark{{ID: "bug-hunt-bench", Version: "2026-09-13", Publisher: "bug-hunt", Categories: []CategoryCoverage{{"code.fix", "planted bug fixes verified blind"}}, Metrics: []Metric{{Name: "fixed", Unit: "bugs", Direction: "higher_better", Scale: &Scale{0, float64(benchdata.BugHuntBenchTotal)}, Description: "planted bugs fixed"}, {Name: "list_cost", Unit: "usd", Direction: "lower_better", Description: "list cost of the bench run"}}, GradingMethod: "tests"}}
	for _, row := range v.Rows {
		if row.Key == "" || row.ModelName == "" || row.OriginRef == "" || !member(row.Kind, "measured", "interpolated", "cost") {
			return doc, Report{}, refuse("evidence_bughunt_row", "invalid bench row")
		}
		if row.SourceVersion != "" && row.SourceVersion != benchdata.BugHuntSourceVersion {
			return doc, Report{}, refuse("evidence_bughunt_version", "unsupported benchmark source version")
		}
		// Fixed denominators describe the common benchmark scale. Only measured
		// fixed rows also have a sample count; cost has neither denominator nor scale.
		if row.Kind == "cost" {
			if row.Denominator != nil {
				return doc, Report{}, refuse("evidence_bughunt_denominator", "cost row must not have a denominator")
			}
		} else if row.Denominator == nil || *row.Denominator != int64(benchdata.BugHuntBenchTotal) {
			return doc, Report{}, refuse("evidence_bughunt_denominator", "fixed row denominator must equal the declared benchmark scale")
		}
		model := row.ModelName
		resolution := "unresolved"
		for _, m := range v.Mapping {
			if m.ExternalName == row.ModelName {
				model = m.ModelID
				if r.Resolves(model) {
					resolution = "resolved"
				}
				break
			}
		}
		o := Observation{BenchmarkRef: Reference{"bug-hunt-bench", "2026-09-13"}, Subject: Fingerprint{ModelID: model, Effort: row.Effort}, SubjectResolution: resolution, Categories: []string{"code.fix"}, Metric: "fixed", Value: row.Value, EvidenceKind: row.Kind, GradingMethod: "tests", Cost: row.Cost, Provenance: Provenance{"bug-hunt", v.RetrievedAt, "module:" + ModuleVersion + "/bench/" + row.Key, row.OriginRef}, ObservedAt: canonical.Unknown}
		if row.Kind == "cost" {
			if row.Cost == nil || row.Cost.USD == nil {
				return doc, Report{}, refuse("evidence_bughunt_cost", "cost row requires explicit usd")
			}
			o.EvidenceKind = "measured"
			o.Metric = "list_cost"
			if row.Value != *row.Cost.USD {
				return doc, Report{}, refuse("evidence_bughunt_cost", "cost value disagrees with usd")
			}
		}
		if row.Kind == "measured" {
			count := *row.Denominator
			o.SampleCount = &count
		}
		doc.Observations = append(doc.Observations, o)
	}
	if len(doc.Observations) != len(v.Rows) {
		return doc, Report{}, refuse("evidence_bughunt_parity", fmt.Sprintf("expected %d rows", len(v.Rows)))
	}
	return Prepare(doc, r)
}
