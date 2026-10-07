package evidence

import (
	"encoding/json"
	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func (r Registry) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.Reference.Name == "" || r.Reference.Version == "" || r.Models == nil {
		return refuse("evidence_invalid_registry", "invalid frozen registry")
	}
	if err := canonical.CheckOrdered(r.Models, func(m RegistryModel) string { return m.ModelID }); err != nil {
		return err
	}
	for _, m := range r.Models {
		if !known(m.ModelID) || m.Efforts == nil {
			return refuse("evidence_invalid_registry", "invalid registry model")
		}
		if err := stringSet(m.Efforts); err != nil {
			return err
		}
	}
	return nil
}
func (r Registry) Resolves(model string) bool {
	for _, m := range r.Models {
		if m.ModelID == model {
			return true
		}
	}
	return false
}
func (r Registry) Accepts(model, effort string) bool {
	if !known(effort) {
		return false
	}
	for _, m := range r.Models {
		if m.ModelID == model {
			for _, e := range m.Efforts {
				if e == effort {
					return true
				}
			}
		}
	}
	return false
}

// Native validates and explicitly normalises declared keyed sets. Input is
// cloned before changes, so callers' values and earlier imports stay immutable.
func Native(raw []byte, r Registry) (Import, Report, error) {
	var v Import
	if err := Decode(raw, &v); err != nil {
		return v, Report{}, err
	}
	return Prepare(v, r)
}

// Prepare retains source-declared resolution; registry resolution is derived
// for each snapshot and never changes an import or its record addresses.
func Prepare(input Import, r Registry) (Import, Report, error) {
	report := Report{Issues: []Issue{}}
	var v Import
	raw, err := canonical.Marshal(input)
	if err != nil {
		return v, report, err
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		return v, report, err
	}
	if err = r.Validate(); err != nil {
		return v, report, err
	}
	// Detect explicit cycles before verifying supplied content addresses. This is
	// also a defensive refusal for future address migrations.
	allAddressed := true
	for _, o := range v.Observations {
		if o.ID == "" {
			allAddressed = false
		}
	}
	for _, n := range v.Notes {
		if n.ID == "" {
			allAddressed = false
		}
	}
	for _, t := range v.Retractions {
		if t.ID == "" {
			allAddressed = false
		}
	}
	if allAddressed {
		if _, e := Resolve([]Import{v}); e != nil {
			return v, report, e
		}
	}
	cats := TaxonomyV1().Categories
	category := func(c, ref string) {
		found := false
		for _, tc := range cats {
			if c == tc.ID {
				found = true
			}
		}
		if !found {
			report.Issues = append(report.Issues, Issue{"category_unknown", ref, c})
		}
	}
	sortStrings := func(s []string) { canonical.SortByKey(s, func(x string) string { return x }) }
	sortFacets := func(f *Facets) {
		if f != nil {
			sortStrings(f.Languages)
			sortStrings(f.Platforms)
			sortStrings(f.Roles)
		}
	}
	for i := range v.Benchmarks {
		b := &v.Benchmarks[i]
		canonical.SortByKey(b.Categories, func(c CategoryCoverage) string { return c.Category })
		canonical.SortByKey(b.Metrics, func(m Metric) string { return m.Name })
		for _, c := range b.Categories {
			category(c.Category, b.Address())
		}
	}
	for i := range v.Observations {
		o := &v.Observations[i]
		sortStrings(o.Categories)
		sortStrings(o.Supersedes)
		sortFacets(o.Facets)
		id, err := RecordAddress("obs:", *o)
		if err != nil {
			return v, report, err
		}
		if o.ID != "" && o.ID != id {
			return v, report, refuse("evidence_address_mismatch", "observation id mismatch")
		}
		o.ID = id
		if !observationResolved(*o, r) {
			report.Issues = append(report.Issues, Issue{"model_unresolved", id, o.Subject.ModelID})
		}
		if observationResolved(*o, r) && known(o.Subject.Effort) && !r.Accepts(o.Subject.ModelID, o.Subject.Effort) {
			report.Issues = append(report.Issues, Issue{"effort_unrecognised", id, o.Subject.Effort})
		}
		for _, c := range o.Categories {
			category(c, id)
		}
	}
	for i := range v.Notes {
		n := &v.Notes[i]
		sortStrings(n.Claim.Categories)
		sortStrings(n.Supersedes)
		sortStrings(n.Subject.Efforts)
		sortFacets(n.Claim.Facets)
		canonical.SortByKey(n.EvidenceRefs, func(r EvidenceRef) string { return r.Kind + ":" + r.Ref })
		id, err := RecordAddress("note:", *n)
		if err != nil {
			return v, report, err
		}
		if n.ID != "" && n.ID != id {
			return v, report, refuse("evidence_address_mismatch", "note id mismatch")
		}
		n.ID = id
		if !r.Resolves(n.Subject.ModelID) {
			report.Issues = append(report.Issues, Issue{"model_unresolved", id, n.Subject.ModelID})
		}
		for _, e := range n.Subject.Efforts {
			if r.Resolves(n.Subject.ModelID) && !r.Accepts(n.Subject.ModelID, e) {
				report.Issues = append(report.Issues, Issue{"effort_unrecognised", id, e})
			}
		}
		for _, c := range n.Claim.Categories {
			category(c, id)
		}
	}
	for i := range v.Retractions {
		t := &v.Retractions[i]
		id, err := RecordAddress("ret:", *t)
		if err != nil {
			return v, report, err
		}
		if t.ID != "" && t.ID != id {
			return v, report, refuse("evidence_address_mismatch", "retraction id mismatch")
		}
		t.ID = id
	}
	canonical.SortByKey(v.Benchmarks, func(b Benchmark) string { return b.Address() })
	canonical.SortByKey(v.Observations, func(o Observation) string { return o.ID })
	canonical.SortByKey(v.Notes, func(n Note) string { return n.ID })
	canonical.SortByKey(v.Retractions, func(t Retraction) string { return t.ID })
	canonical.SortByKey(report.Issues, func(i Issue) string { return i.Code + "\x00" + i.Ref + "\x00" + i.Detail })
	return v, report, v.Validate()
}
func EmptyImport(source string, importer Identity, at string) Import {
	return Import{SchemaVersion: SchemaVersion, Kind: "evidence-import", Source: source, Importer: importer, ImportedAt: at, MeasuredBy: "internal", Benchmarks: []Benchmark{}, Observations: []Observation{}, Notes: []Note{}, Retractions: []Retraction{}}
}
func NoteImport(n Note, at string, r Registry) (Import, Report, error) {
	v := EmptyImport("operator-notes", Identity{"notes", "1"}, at)
	v.Notes = append(v.Notes, n)
	return Prepare(v, r)
}
func RetractionImport(t Retraction, at string, r Registry) (Import, Report, error) {
	v := EmptyImport("operator-notes", Identity{"notes", "1"}, at)
	v.Retractions = append(v.Retractions, t)
	return Prepare(v, r)
}

// observationResolved preserves an explicit source refusal even when the bound
// registry knows the model. Otherwise resolution follows that registry.
func observationResolved(o Observation, r Registry) bool {
	return o.SubjectResolution == "resolved" && r.Resolves(o.Subject.ModelID)
}

// ResolveSnapshot derives status and resolution without rewriting stored imports.
func ResolveSnapshot(imports []Import, r Registry) (ActiveSet, error) {
	a, err := Resolve(imports)
	if err != nil {
		return a, err
	}
	for i := range a.Records {
		rec := &a.Records[i]
		if rec.Observation != nil && !observationResolved(*rec.Observation, r) {
			rec.Observation.SubjectResolution = "unresolved"
		}
	}
	return a, nil
}
