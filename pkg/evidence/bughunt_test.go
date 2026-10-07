package evidence

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/skill-agents-management/pkg/vendorplugin/benchdata"
)

// CMR_UPDATE_EVIDENCE=1 rewrites reviewed new-pin vectors only. An environment
// switch works with the required evidence and CLI packages in the same command.

func pinnedBugHunt(t *testing.T) BugHuntExport {
	t.Helper()
	v, err := PinnedExport()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// This fixture and canonical import were captured from the unchanged v0.5.32
// importer before replacing it. They are never rewritten during vector regeneration.
// Reset ONLY raw_artifact_ref to the old pin, then recompute record addresses:
// canonical equality proves the accessor path preserves the same source facts.
func sameSourceBugHunt(t *testing.T) (BugHuntExport, Import) {
	t.Helper()
	var old BugHuntExport
	fixture(t, "bughunt-v0.5.32-export.json", &old)
	if old.ModuleVersion != "v0.5.32" || len(old.Rows) != 58 {
		t.Fatal("invalid legacy baseline")
	}
	current := pinnedBugHunt(t)
	if !reflect.DeepEqual(current.Models, old.Models) || !reflect.DeepEqual(current.Mapping, old.Mapping) || current.MappingVersion != old.MappingVersion {
		t.Fatal("same-source registry or mapping facts changed")
	}
	if len(current.Rows) != len(old.Rows) {
		t.Fatal("same-source claim count changed")
	}
	for i, row := range current.Rows {
		row.Denominator, row.SourceVersion = nil, ""
		if !reflect.DeepEqual(row, old.Rows[i]) {
			t.Fatalf("same-source fact changed: %s", row.Key)
		}
	}
	doc, _, err := ImportBugHunt(current, "")
	if err != nil {
		t.Fatal(err)
	}
	legacy := doc
	legacy.Observations = append([]Observation(nil), doc.Observations...)
	for i := range legacy.Observations {
		o := &legacy.Observations[i]
		prefix := "module:" + ModuleVersion + "/bench/"
		if !strings.HasPrefix(o.Provenance.RawArtifactRef, prefix) {
			t.Fatal("unexpected artifact identity")
		}
		o.Provenance.RawArtifactRef = "module:" + old.ModuleVersion + "/bench/" + strings.TrimPrefix(o.Provenance.RawArtifactRef, prefix)
		o.ID = ""
	}
	legacy, _, err = Prepare(legacy, old.Registry())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := canonical.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/bughunt-v0.5.32-import.canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, golden) {
		t.Fatal("same-source canonical observations/import drifted from v0.5.32")
	}
	digest, err := legacy.Digest()
	if err != nil || digest != "sha256:5dfa8f4005a38dccacd56f6a401e37279ffae185f140909e3843e60c52f045bd" {
		t.Fatalf("legacy baseline drift: %s %v", digest, err)
	}
	return old, doc
}

func TestBugHuntSameSourceParity(t *testing.T) { sameSourceBugHunt(t) }

func TestAcceptance2BugHuntParity(t *testing.T) {
	v := pinnedBugHunt(t)
	doc, _, err := ImportBugHunt(v, "")
	if err != nil {
		t.Fatal(err)
	}
	rows := benchdata.BugHuntRows()
	if len(rows) != 58 || len(v.Rows) != len(rows) || len(doc.Observations) != len(rows) {
		t.Fatalf("claim counts: accessor=%d export=%d import=%d", len(rows), len(v.Rows), len(doc.Observations))
	}
	actual := map[string]Observation{}
	prefix := "module:" + ModuleVersion + "/bench/"
	for _, o := range doc.Observations {
		if !strings.HasPrefix(o.Provenance.RawArtifactRef, prefix) {
			t.Fatalf("unexpected artifact: %s", o.Provenance.RawArtifactRef)
		}
		key := strings.TrimPrefix(o.Provenance.RawArtifactRef, prefix)
		if _, duplicate := actual[key]; duplicate {
			t.Fatalf("duplicate source key: %s", key)
		}
		actual[key] = o
	}
	expected := map[string]bool{}
	counts := map[string]int{}
	for _, row := range rows {
		if expected[row.Key] {
			t.Fatalf("duplicate accessor key: %s", row.Key)
		}
		expected[row.Key] = true
		counts[row.Kind]++
		t.Run(row.Key, func(t *testing.T) {
			o, found := actual[row.Key]
			if !found {
				t.Fatal("accessor row dropped")
			}
			kind, metric := row.Kind, "fixed"
			if kind == "cost" {
				kind, metric = "measured", "list_cost"
			}
			if o.Subject != (Fingerprint{ModelID: row.ModelID, Effort: row.Effort}) || o.SubjectResolution != "resolved" || o.EvidenceKind != kind || o.Metric != metric || o.Value != row.Value {
				t.Fatalf("row drift: %+v", o)
			}
			if o.BenchmarkRef.Address() != row.SourceVersion || o.Provenance.OriginRef != row.OriginRef || o.Provenance.Source != "bug-hunt" || o.Provenance.RetrievedAt != v.RetrievedAt || o.ObservedAt != canonical.Unknown {
				t.Fatalf("source/origin/time drift: %+v", o)
			}
			var cost *Cost
			if row.Cost != nil {
				cost = &Cost{TokensIn: row.Cost.TokensIn, TokensOut: row.Cost.TokensOut, USD: row.Cost.USD, WallS: row.Cost.WallS}
			}
			if !reflect.DeepEqual(o.Cost, cost) {
				t.Fatalf("cost drift: %+v != %+v", o.Cost, cost)
			}
			if row.Kind == "cost" {
				if row.Denominator != nil || o.SampleCount != nil {
					t.Fatal("cost acquired a denominator/sample count")
				}
			} else {
				if row.Denominator == nil || *row.Denominator != 105 {
					t.Fatal("invalid accessor fixed denominator")
				}
				if row.Kind == "measured" {
					if o.SampleCount == nil || *o.SampleCount != *row.Denominator {
						t.Fatal("measured sample count lost")
					}
				} else if o.SampleCount != nil || o.Subject.Effort != "" {
					t.Fatal("interpolation acquired sample count or effort")
				}
			}
		})
	}
	if counts["measured"] != 12 || counts["interpolated"] != 45 || counts["cost"] != 1 || len(counts) != 3 {
		t.Fatalf("claim kinds: %v", counts)
	}
	for key := range actual {
		if !expected[key] {
			t.Fatalf("extra observation key: %s", key)
		}
	}
	if len(actual) != len(expected) {
		t.Fatal("source key sets differ")
	}
	metrics := map[string]int{}
	for _, o := range doc.Observations {
		metrics[o.Metric+"/"+o.EvidenceKind]++
	}
	if !reflect.DeepEqual(metrics, map[string]int{"fixed/measured": 12, "fixed/interpolated": 45, "list_cost/measured": 1}) {
		t.Fatalf("metric/kind semantics: %v", metrics)
	}
	for _, metric := range doc.Benchmarks[0].Metrics {
		if metric.Name == "fixed" && !reflect.DeepEqual(metric.Scale, &Scale{Min: 0, Max: 105}) {
			t.Fatal("fixed scale drift")
		}
		if metric.Name == "list_cost" && metric.Scale != nil {
			t.Fatal("cost acquired a quality scale")
		}
	}
	facts := benchdata.RegistryFacts()
	if len(v.Models) != len(facts) || len(v.Mapping) != len(facts) {
		t.Fatal("registry projection incomplete")
	}
	for i, fact := range facts {
		t.Run("registry/"+fact.ModelID, func(t *testing.T) {
			if !reflect.DeepEqual(v.Models[i], RegistryModel{ModelID: fact.ModelID, Efforts: fact.Efforts}) || v.Mapping[i] != (ModelMapping{ExternalName: fact.ModelID, ModelID: fact.ModelID}) {
				t.Fatal("registry or explicit identity mapping drift")
			}
		})
	}
}

type bugHuntVectorRow struct {
	Key       string `json:"key"`
	Address   string `json:"address"`
	OriginRef string `json:"origin_ref"`
}
type bugHuntVectors struct {
	SchemaVersion  string             `json:"schema_version"`
	ModuleVersion  string             `json:"module_version"`
	ExportDigest   string             `json:"export_digest"`
	ImportDigest   string             `json:"import_digest"`
	RegistryDigest string             `json:"registry_digest"`
	SnapshotDigest string             `json:"snapshot_digest"`
	Rows           []bugHuntVectorRow `json:"rows"`
}

func TestBugHuntPinnedVectors(t *testing.T) {
	// Even regeneration must first prove equality with the frozen old source.
	_, doc := sameSourceBugHunt(t)
	v := pinnedBugHunt(t)
	mustDigest := func(value any) string {
		t.Helper()
		digest, err := canonical.Digest(value)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	s, err := NewStore(t.TempDir(), v.Registry())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Add(doc)
	if err != nil {
		t.Fatal(err)
	}
	vectors := bugHuntVectors{SchemaVersion: SchemaVersion, ModuleVersion: ModuleVersion, ExportDigest: mustDigest(v), ImportDigest: result.ImportDigest, RegistryDigest: mustDigest(v.Registry()), SnapshotDigest: result.SnapshotDigest, Rows: []bugHuntVectorRow{}}
	for _, o := range doc.Observations {
		key := strings.TrimPrefix(o.Provenance.RawArtifactRef, "module:"+ModuleVersion+"/bench/")
		vectors.Rows = append(vectors.Rows, bugHuntVectorRow{Key: key, Address: o.ID, OriginRef: o.Provenance.OriginRef})
	}
	canonical.SortByKey(vectors.Rows, func(row bugHuntVectorRow) string { return row.Key })
	for _, tc := range []struct {
		path  string
		value any
	}{
		{"data/bughunt-export.json", v},
		{"testdata/bughunt-v0.5.40-import.canonical.json", doc},
		{"testdata/bughunt-v0.5.40-vectors.json", vectors},
	} {
		t.Run(tc.path, func(t *testing.T) {
			raw, err := canonical.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if os.Getenv("CMR_UPDATE_EVIDENCE") == "1" {
				if err := os.WriteFile(tc.path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			golden, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, golden) {
				t.Fatalf("pinned vector changed: %s; review facts and new-source identities", tc.path)
			}
		})
	}
	t.Logf("v%s vectors: export=%s import=%s registry=%s snapshot=%s", strings.TrimPrefix(ModuleVersion, "v"), vectors.ExportDigest, vectors.ImportDigest, vectors.RegistryDigest, vectors.SnapshotDigest)
	manifest, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(manifest))
	pin := ""
	for i, field := range fields {
		if field == "github.com/relux-works/skill-agents-management" && i+1 < len(fields) {
			pin = fields[i+1]
		}
	}
	if pin != ModuleVersion {
		t.Fatalf("dependency/importer pin mismatch: %s != %s", pin, ModuleVersion)
	}
}

func TestBugHuntTypedRefusals(t *testing.T) {
	ptr := func(v int64) *int64 { return &v }
	cases := []struct {
		name, code string
		change     func(*BugHuntExport)
	}{
		{"module", "evidence_bughunt_version", func(v *BugHuntExport) { v.ModuleVersion = "v0.5.32" }},
		{"mapping", "evidence_bughunt_version", func(v *BugHuntExport) { v.MappingVersion = "undeclared" }},
		{"schema", "evidence_bughunt_version", func(v *BugHuntExport) { v.SchemaVersion = "other" }},
		{"source", "evidence_bughunt_version", func(v *BugHuntExport) { v.Rows[0].SourceVersion = "other@1" }},
		{"retrieved-at", "evidence_invalid_time", func(v *BugHuntExport) { v.RetrievedAt = "unknown" }},
		{"imported-at", "evidence_invalid_time", func(v *BugHuntExport) { v.ImportedAt = "unknown" }},
		{"row-model", "evidence_bughunt_row", func(v *BugHuntExport) { v.Rows[0].ModelName = "" }},
		{"row-origin", "evidence_bughunt_row", func(v *BugHuntExport) { v.Rows[0].OriginRef = "" }},
		{"row-key", "evidence_bughunt_row", func(v *BugHuntExport) { v.Rows[0].Key = "" }},
		{"row-kind", "evidence_bughunt_row", func(v *BugHuntExport) { v.Rows[0].Kind = "invented" }},
	}
	for _, kind := range []string{"measured", "interpolated"} {
		for _, denominator := range []struct {
			name  string
			value *int64
		}{{"missing", nil}, {"negative", ptr(-1)}, {"zero", ptr(0)}, {"inconsistent", ptr(106)}} {
			cases = append(cases, struct {
				name, code string
				change     func(*BugHuntExport)
			}{kind + "/" + denominator.name, "evidence_bughunt_denominator", func(v *BugHuntExport) {
				for i := range v.Rows {
					if v.Rows[i].Kind == kind {
						v.Rows[i].Denominator = denominator.value
						return
					}
				}
			}})
		}
	}
	for _, cost := range []struct {
		name, code string
		change     func(*BugHuntRow)
	}{
		{"denominator-zero", "evidence_bughunt_denominator", func(r *BugHuntRow) { r.Denominator = ptr(0) }},
		{"denominator-fixed", "evidence_bughunt_denominator", func(r *BugHuntRow) { r.Denominator = ptr(105) }},
		{"missing-cost", "evidence_bughunt_cost", func(r *BugHuntRow) { r.Cost = nil }},
		{"missing-usd", "evidence_bughunt_cost", func(r *BugHuntRow) { r.Cost.USD = nil }},
		{"disagrees", "evidence_bughunt_cost", func(r *BugHuntRow) { r.Value++ }},
	} {
		cases = append(cases, struct {
			name, code string
			change     func(*BugHuntExport)
		}{"cost/" + cost.name, cost.code, func(v *BugHuntExport) {
			for i := range v.Rows {
				if v.Rows[i].Kind == "cost" {
					cost.change(&v.Rows[i])
					return
				}
			}
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := pinnedBugHunt(t)
			tc.change(&v)
			_, _, err := ImportBugHunt(v, "")
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != tc.code {
				t.Fatalf("wanted typed %s, got %v", tc.code, err)
			}
		})
	}
}

func TestBugHuntZeroAndUnknownCost(t *testing.T) {
	for _, tc := range []struct {
		name string
		zero bool
	}{{"zero-cost", true}, {"absent-quality-cost", false}} {
		t.Run(tc.name, func(t *testing.T) {
			v := pinnedBugHunt(t)
			index := 0
			if tc.zero {
				for i := range v.Rows {
					if v.Rows[i].Kind == "cost" {
						index = i
						zero := 0.0
						v.Rows[i].Value = zero
						v.Rows[i].Cost.USD = &zero
						break
					}
				}
			} else {
				v.Rows[index].Cost = nil
			}
			doc, _, err := ImportBugHunt(v, "")
			if err != nil {
				t.Fatal(err)
			}
			ref := "module:" + ModuleVersion + "/bench/" + v.Rows[index].Key
			for _, o := range doc.Observations {
				if o.Provenance.RawArtifactRef == ref {
					if tc.zero {
						if o.Metric != "list_cost" || o.Value != 0 || o.Cost == nil || o.Cost.USD == nil || *o.Cost.USD != 0 {
							t.Fatal("explicit zero cost lost")
						}
					} else if o.Cost != nil {
						t.Fatal("unknown cost fabricated")
					}
					return
				}
			}
			t.Fatal("row dropped")
		})
	}
}

func TestBugHuntPinnedCopiesAndConcurrency(t *testing.T) {
	expected := pinnedBugHunt(t)
	for _, tc := range []struct {
		name   string
		mutate func(*BugHuntExport)
	}{
		{"denominator", func(v *BugHuntExport) { *v.Rows[0].Denominator = 1 }},
		{"cost", func(v *BugHuntExport) {
			for i := range v.Rows {
				if v.Rows[i].Cost != nil {
					*v.Rows[i].Cost.USD = 0
				}
			}
		}},
		{"model-efforts", func(v *BugHuntExport) { v.Models[0].Efforts[0] = "corrupt" }},
		{"mapping", func(v *BugHuntExport) { v.Mapping[0].ModelID = "corrupt" }},
		{"row", func(v *BugHuntExport) { v.Rows[0].OriginRef = "corrupt" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := pinnedBugHunt(t)
			tc.mutate(&v)
			if !reflect.DeepEqual(pinnedBugHunt(t), expected) {
				t.Fatal("export shares mutable data")
			}
		})
	}
	r, err := PinnedRegistry()
	if err != nil {
		t.Fatal(err)
	}
	r.Models[0].Efforts[0] = "corrupt"
	fresh, err := PinnedRegistry()
	if err != nil || !reflect.DeepEqual(fresh, expected.Registry()) {
		t.Fatal("registry shares mutable data")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				v, err := PinnedExport()
				if err != nil || !reflect.DeepEqual(v, expected) {
					t.Errorf("concurrent export drift: %v", err)
					return
				}
				*v.Rows[0].Denominator = 1
				v.Models[0].Efforts[0] = "corrupt"
				for i := range v.Rows {
					if v.Rows[i].Cost != nil {
						*v.Rows[i].Cost.USD = 0
					}
				}
			}
		}()
	}
	wg.Wait()
}

func TestBugHuntUpgradeRebindReplay(t *testing.T) {
	old, doc := sameSourceBugHunt(t)
	var legacy Import
	fixture(t, "bughunt-v0.5.32-import.canonical.json", &legacy)
	root := t.TempDir()
	before, err := NewStore(root, old.Registry())
	if err != nil {
		t.Fatal(err)
	}
	initial, err := before.Add(legacy)
	if err != nil {
		t.Fatal(err)
	}
	oldSnap, _, err := before.Current()
	if err != nil {
		t.Fatal(err)
	}
	next, err := NewStore(root, pinnedBugHunt(t).Registry())
	if err != nil {
		t.Fatal(err)
	}
	_, err = next.Add(doc)
	codeIs(t, err, "evidence_registry_mismatch")
	rebound, err := next.Rebind()
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(rebound, oldSnap) || !reflect.DeepEqual(rebound.Imports, oldSnap.Imports) {
		t.Fatal("rebind must change registry binding only")
	}
	newResult, err := next.Add(doc)
	if err != nil {
		t.Fatal(err)
	}
	if newResult.ImportDigest == initial.ImportDigest || newResult.SnapshotDigest == initial.SnapshotDigest {
		t.Fatal("new source pin must change addresses")
	}
	for _, tc := range []struct {
		name     string
		snap     Snapshot
		registry Registry
	}{{"old", oldSnap, old.Registry()}, {"rebound", rebound, pinnedBugHunt(t).Registry()}} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := next.RegistryFor(tc.snap)
			if err != nil || !reflect.DeepEqual(r, tc.registry) {
				t.Fatalf("old registry not replayable: %v", err)
			}
			docs, _, err := next.InspectSnapshot(tc.snap)
			if err != nil || len(docs) != 1 || !reflect.DeepEqual(docs[0], legacy) {
				t.Fatalf("legacy import changed: %v", err)
			}
		})
	}
	// Derivation must deduplicate an original publication after both pins are
	// present, rather than treating their changed addresses as corroboration.
	latest, _, docs, _, err := next.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	view, err := Derive(latest, docs, pinnedBugHunt(t).Registry(), oneCategory(t, "code.fix"), DefaultDerivation(), []Fingerprint{{ModelID: "gpt-6-astra", Effort: "max"}}, bugHuntReleaseAt)
	if err != nil || len(view.Results) != 1 || len(view.Results[0].Contributions) != 1 {
		t.Fatalf("same publication counted again across pins: %+v %v", view.Results, err)
	}
	// Aliases/reimports share publication identities across pins: they cannot
	// become independent corroboration merely because their record IDs changed.
	origins := map[string]string{}
	addresses := map[string]string{}
	for _, o := range legacy.Observations {
		key := strings.TrimPrefix(o.Provenance.RawArtifactRef, "module:v0.5.32/bench/")
		origins[key] = o.Provenance.OriginRef
		addresses[key] = o.ID
	}
	for _, o := range doc.Observations {
		key := strings.TrimPrefix(o.Provenance.RawArtifactRef, "module:"+ModuleVersion+"/bench/")
		if addresses[key] == o.ID {
			t.Fatalf("new source pin retained an old address: %s", key)
		}
		if origins[key] != o.Provenance.OriginRef {
			t.Fatalf("origin changed across pins: %s", key)
		}
	}
	for _, alias := range []struct{ a, b string }{{"astra:0", "gpt-6-astra:0"}, {"gemini-flash:0", "gemini-3.8-flash-high:0"}, {"muse-spark:0", "muse-spark-1.3-contributor:0"}} {
		if origins[alias.a] != origins[alias.b] {
			t.Fatalf("alias publication split: %s", alias.a)
		}
	}
}

func TestBugHuntJSONInterchange(t *testing.T) {
	for _, tc := range []struct {
		name       string
		omitSource bool
	}{{"accessor-export", false}, {"implicit-pinned-source", true}} {
		t.Run(tc.name, func(t *testing.T) {
			v := pinnedBugHunt(t)
			if tc.omitSource {
				for i := range v.Rows {
					v.Rows[i].SourceVersion = ""
				}
			}
			raw, err := canonical.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			decoded, _, err := BugHunt(raw, "")
			if err != nil {
				t.Fatal(err)
			}
			direct, _, err := ImportBugHunt(pinnedBugHunt(t), "")
			if err != nil || !reflect.DeepEqual(decoded, direct) {
				t.Fatalf("public JSON workflow changed observations: %v", err)
			}
		})
	}
}
