package evidence

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

const round2At = "2026-10-01T00:00:00Z"

type round2Vectors struct {
	SchemaVersion string   `json:"schema_version"`
	Efforts       []string `json:"efforts"`
	Derivation    []struct {
		Name      string   `json:"name"`
		Direction string   `json:"direction"`
		Value     float64  `json:"value"`
		Score     *float64 `json:"score,omitempty"`
		Coverage  string   `json:"coverage"`
		Grade     string   `json:"grade"`
	} `json:"derivation"`
	NoteTimes []struct {
		Name        string `json:"name"`
		CreatedAt   string `json:"created_at"`
		EvaluatedAt string `json:"evaluated_at"`
		Coverage    string `json:"coverage"`
	} `json:"note_times"`
}

func round2Data(t *testing.T) round2Vectors {
	t.Helper()
	var v round2Vectors
	fixture(t, "round2.json", &v)
	return v
}
func observationDoc(t *testing.T, r Registry, o Observation, b Benchmark) Import {
	t.Helper()
	doc := EmptyImport("fixture", Identity{"native", "1"}, round2At)
	doc.Observations, doc.Benchmarks = []Observation{o}, []Benchmark{b}
	doc, _, err := Prepare(doc, r)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func oneCategory(t *testing.T, category string) RequirementsInput {
	t.Helper()
	req, err := RoleRequirements("developer", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	req.Requirements = []Requirement{{Category: category, Weight: 1}}
	return req
}
func noContribution(t *testing.T, v View) {
	t.Helper()
	s := v.Results[0]
	if s.Grade != "unknown" || s.Score != nil || len(s.Contributions) != 0 || s.Coverage[0].Kind != "none" || s.Coverage[0].Match != "none" {
		t.Fatalf("unexpected evidence: %+v", s)
	}
}
func TestAcceptance3EqualUnknownEfforts(t *testing.T) {
	r := testRegistry(t)
	for _, effort := range round2Data(t).Efforts {
		t.Run("effort="+effort, func(t *testing.T) {
			candidate := testCandidate(t)
			candidate.Effort = effort
			o, b := testObservation(t)
			o.ID, o.Subject = "", candidate
			doc := observationDoc(t, r, o, b)
			if ok, match := Match(doc.Observations[0].Subject, candidate, r); ok || match != "none" {
				t.Fatalf("equal unknown efforts matched: %v %s", ok, match)
			}
			v, err := Derive(snapshotFor(t, r, []Import{doc}), []Import{doc}, r, oneCategory(t, "code.fix"), DefaultDerivation(), []Fingerprint{candidate}, round2At)
			if err != nil {
				t.Fatal(err)
			}
			noContribution(t, v)
		})
	}
}
func TestAcceptance4SourceUnresolvedKnownModel(t *testing.T) {
	r := testRegistry(t)
	candidate := testCandidate(t)
	o, b := testObservation(t)
	o.ID, o.Subject, o.SubjectResolution = "", candidate, "unresolved"
	doc := observationDoc(t, r, o, b)
	if len(doc.Observations) != 1 || doc.Observations[0].SubjectResolution != "unresolved" {
		t.Fatal("source unresolved observation lost")
	}
	s, err := NewStore(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(doc); err != nil {
		t.Fatal(err)
	}
	unresolved, err := s.Unresolved()
	if err != nil || len(unresolved.Issues) != 1 || unresolved.Issues[0].Code != "model_unresolved" || unresolved.Issues[0].Ref != doc.Observations[0].ID {
		t.Fatalf("unresolved=%+v err=%v", unresolved, err)
	}
	v, err := Derive(snapshotFor(t, r, []Import{doc}), []Import{doc}, r, oneCategory(t, "code.fix"), DefaultDerivation(), []Fingerprint{candidate}, round2At)
	if err != nil {
		t.Fatal(err)
	}
	noContribution(t, v)
}
func TestAcceptance6Round2Derivation(t *testing.T) {
	r := testRegistry(t)
	candidate := testCandidate(t)
	for _, tc := range round2Data(t).Derivation {
		t.Run(tc.Name, func(t *testing.T) {
			o, b := testObservation(t)
			o.ID, o.Subject, o.Value, o.Metric = "", candidate, tc.Value, "latency"
			b.Metrics = []Metric{{Name: "latency", Unit: "s", Direction: "lower_better", Description: "completion latency"}}
			doc := observationDoc(t, r, o, b)
			d := DefaultDerivation()
			d.Name, d.Version = "latency-vector", "1"
			d.Normalisations = []Normalisation{}
			if tc.Direction != "unmapped" {
				d.Normalisations = []Normalisation{{BenchmarkRef: o.BenchmarkRef, Metric: o.Metric, Min: 0, Max: 105, Direction: tc.Direction, Categories: []string{"code.fix"}}}
			}
			v, err := Derive(snapshotFor(t, r, []Import{doc}), []Import{doc}, r, oneCategory(t, "code.fix"), d, []Fingerprint{candidate}, round2At)
			if err != nil {
				t.Fatal(err)
			}
			result := v.Results[0]
			if result.Grade != tc.Grade || result.Coverage[0].Kind != tc.Coverage || (result.Score == nil) != (tc.Score == nil) {
				t.Fatalf("result=%+v", result)
			}
			if tc.Score != nil && (math.Abs(*result.Score-*tc.Score) > 1e-12 || len(result.Contributions) != 1 || math.Abs(result.Contributions[0].Weight-*tc.Score) > 1e-12) {
				t.Fatalf("lower_better not inverted: %+v", result)
			}
			if tc.Direction == "unmapped" {
				noContribution(t, v)
				if len(v.Issues) != 1 || v.Issues[0].Code != "observation_unmapped" || v.Issues[0].Ref != doc.Observations[0].ID || !strings.HasSuffix(v.Issues[0].Detail, "/latency/code.fix") {
					t.Fatalf("missing unmapped issue: %+v", v.Issues)
				}
			}
		})
	}
}
func TestAcceptance6FutureNotes(t *testing.T) {
	r := testRegistry(t)
	for _, tc := range round2Data(t).NoteTimes {
		t.Run(tc.Name, func(t *testing.T) {
			n := testNote(t)
			n.ID, n.CreatedAt = "", tc.CreatedAt
			doc, _, err := NoteImport(n, round2At, r)
			if err != nil {
				t.Fatal(err)
			}
			v, err := Derive(snapshotFor(t, r, []Import{doc}), []Import{doc}, r, oneCategory(t, "review.code"), DefaultDerivation(), []Fingerprint{testCandidate(t)}, tc.EvaluatedAt)
			if err != nil {
				t.Fatal(err)
			}
			if v.Results[0].Coverage[0].Kind != tc.Coverage {
				t.Fatalf("future note used: %+v", v.Results[0])
			}
			if tc.Coverage == "none" {
				noContribution(t, v)
			} else if len(v.Results[0].Contributions) != 1 || v.Results[0].Contributions[0].AgeS == nil || *v.Results[0].Contributions[0].AgeS != 0 {
				t.Fatal("creation boundary excluded")
			}
		})
	}
}
func TestAcceptance5CrossImportCycle(t *testing.T) {
	r := testRegistry(t)
	s, err := NewStore(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	future := "note:" + strings.Repeat("1", 64)
	a := testNote(t)
	a.ID, a.Supersedes = "", []string{future}
	first, _, err := NoteImport(a, round2At, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(first); err != nil {
		t.Fatal(err)
	}
	_, before, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	b := testNote(t)
	b.ID, b.Supersedes, b.Claim.Statement = future, []string{first.Notes[0].ID}, "closes cycle"
	second := EmptyImport("fixture", Identity{"notes", "1"}, round2At)
	second.Notes = []Note{b}
	_, err = s.Add(second)
	codeIs(t, err, "evidence_cycle")
	_, after, err := s.Current()
	if err != nil || after != before {
		t.Fatal("refused cycle changed current snapshot")
	}
}
func TestAcceptance5ObservationLifecycle(t *testing.T) {
	r := testRegistry(t)
	o, b := testObservation(t)
	o.ID = ""
	a := observationDoc(t, r, o, b)
	o.Supersedes = []string{a.Observations[0].ID}
	o.Value = 43
	middle := observationDoc(t, r, o, b)
	o.Supersedes = []string{middle.Observations[0].ID}
	o.Value = 44
	head := observationDoc(t, r, o, b)
	for _, tc := range []struct {
		name, target string
		active       []string
	}{
		{"chain", "", []string{head.Observations[0].ID}},
		{"retract-head", head.Observations[0].ID, []string{middle.Observations[0].ID}},
		{"retract-middle", middle.Observations[0].ID, []string{a.Observations[0].ID, head.Observations[0].ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := []Import{a, middle, head}
			if tc.target != "" {
				ret, _, err := RetractionImport(Retraction{Target: tc.target, Reason: "withdrawn", Author: Author{"human", "operator"}, At: round2At}, round2At, r)
				if err != nil {
					t.Fatal(err)
				}
				docs = append(docs, ret)
			}
			canonical.SortByKey(tc.active, func(s string) string { return s })
			for _, order := range permutations(docs) {
				active, err := Resolve(order)
				if err != nil {
					t.Fatal(err)
				}
				ids := []string{}
				for _, rec := range active.Records {
					if rec.Observation != nil && rec.Status == "active" {
						ids = append(ids, rec.ID)
					}
					if rec.ID == tc.target && rec.Status != "retracted" {
						t.Fatal("observation not withdrawn")
					}
				}
				if strings.Join(ids, ",") != strings.Join(tc.active, ",") {
					t.Fatalf("active=%v want=%v", ids, tc.active)
				}
			}
			s, err := NewStore(t.TempDir(), r)
			if err != nil {
				t.Fatal(err)
			}
			for _, doc := range docs {
				if _, err = s.Add(doc); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range tc.active {
				rec, err := s.Show(id)
				if err != nil || rec.Status != "active" {
					t.Fatalf("stored observation=%+v %v", rec, err)
				}
			}
		})
	}
}
func TestStoreRegistryRebind(t *testing.T) {
	b := testRegistry(t)
	a := Registry{SchemaVersion, Identity{"fixture", "A"}, []RegistryModel{}}
	root := t.TempDir()
	s, err := NewStore(root, a)
	if err != nil {
		t.Fatal(err)
	}
	o, bench := testObservation(t)
	o.ID, o.Subject = "", testCandidate(t)
	doc := observationDoc(t, a, o, bench)
	if doc.Observations[0].SubjectResolution != "resolved" {
		t.Fatal("registry resolution baked into import")
	}
	first, err := s.Add(doc)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(filepath.Join(root, "snapshots", first.SnapshotDigest+".json"))
	if err != nil {
		t.Fatal(err)
	}
	importsBefore, err := os.ReadFile(filepath.Join(root, "imports", first.ImportDigest+".json"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Unresolved()
	if err != nil || len(u.Issues) != 1 {
		t.Fatalf("A must be unresolved: %+v %v", u, err)
	}
	rebinder, err := NewStore(root, b)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rebinder.Add(doc)
	codeIs(t, err, "evidence_registry_mismatch")
	if !strings.Contains(err.Error(), "rebind") {
		t.Fatal("registry mismatch must name rebind")
	}
	newSnap, err := rebinder.Rebind()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := newSnap.Digest()
	if err != nil || sid == first.SnapshotDigest || strings.Join(newSnap.Imports, ",") != strings.Join(old.Imports, ",") {
		t.Fatal("rebind must change binding only")
	}
	for _, tc := range []struct {
		name       string
		snap       Snapshot
		resolution string
		registry   Registry
	}{
		{"old", old, "unresolved", a}, {"new", newSnap, "resolved", b},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs, active, err := rebinder.InspectSnapshot(tc.snap)
			if err != nil || active.Records[0].Observation.SubjectResolution != tc.resolution {
				t.Fatalf("resolution=%+v %v", active, err)
			}
			if docs[0].Observations[0].SubjectResolution != "resolved" {
				t.Fatal("stored source declaration changed")
			}
			v, err := Derive(tc.snap, docs, tc.registry, oneCategory(t, "code.fix"), DefaultDerivation(), []Fingerprint{testCandidate(t)}, round2At)
			if err != nil {
				t.Fatal(err)
			}
			if tc.resolution == "unresolved" {
				noContribution(t, v)
			} else if len(v.Results[0].Contributions) != 1 {
				t.Fatal("rebound evidence did not contribute")
			}
		})
	}
	u, err = rebinder.Unresolved()
	if err != nil || len(u.Issues) != 0 {
		t.Fatalf("B still unresolved: %+v %v", u, err)
	}
	duplicate, err := rebinder.Add(doc)
	if err != nil || duplicate.ImportDigest != first.ImportDigest || !duplicate.AlreadyPresent {
		t.Fatalf("rebind changed import address: %+v %v", duplicate, err)
	}
	for _, tc := range []struct {
		dir, id string
		before  []byte
	}{
		{"snapshots", first.SnapshotDigest, oldBytes}, {"imports", first.ImportDigest, importsBefore},
	} {
		after, err := os.ReadFile(filepath.Join(root, tc.dir, tc.id+".json"))
		if err != nil || string(after) != string(tc.before) {
			t.Fatal("rebind changed immutable bytes")
		}
	}
}
func TestBugHuntPinnedTimeIdempotence(t *testing.T) {
	export, err := PinnedExport()
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := ImportBugHunt(export, "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := ImportBugHunt(export, "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.Digest()
	if err != nil || a != b || first.ImportedAt != export.ImportedAt {
		t.Fatal("pinned import time not stable")
	}
	for i, o := range first.Observations {
		if o.ID != second.Observations[i].ID || o.Provenance.RetrievedAt != export.RetrievedAt {
			t.Fatal("pinned observation address/time not stable")
		}
	}
	s, err := NewStore(t.TempDir(), export.Registry())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(first); err != nil {
		t.Fatal(err)
	}
	again, err := s.Add(second)
	if err != nil || !again.AlreadyPresent || again.Status != "already present" {
		t.Fatalf("duplicate=%+v %v", again, err)
	}
	override, _, err := ImportBugHunt(export, "2026-10-02T00:00:00Z")
	if err != nil || override.ImportedAt == first.ImportedAt || override.Observations[0].ID != first.Observations[0].ID {
		t.Fatal("override must change import time, retain retrieval time")
	}
}

func TestUnresolvedEffortNoNoise(t *testing.T) {
	r := testRegistry(t)
	for _, tc := range []struct{ name, model, resolution string }{
		{"absent-model", "unmapped-model", "resolved"},
		{"source-unresolved", "gpt-6.1-sol", "unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, b := testObservation(t)
			o.ID, o.Subject.ModelID, o.Subject.Effort, o.SubjectResolution = "", tc.model, "ultrahigh", tc.resolution
			doc := EmptyImport("fixture", Identity{"native", "1"}, round2At)
			doc.Benchmarks, doc.Observations = []Benchmark{b}, []Observation{o}
			_, report, err := Prepare(doc, r)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Issues) != 1 || report.Issues[0].Code != "model_unresolved" {
				t.Fatalf("unknown vocabulary noise: %+v", report)
			}
		})
	}
	n := testNote(t)
	n.ID, n.Subject.ModelID, n.Subject.Efforts = "", "unmapped-model", []string{"ultrahigh"}
	_, report, err := NoteImport(n, round2At, r)
	if err != nil || len(report.Issues) != 1 || report.Issues[0].Code != "model_unresolved" {
		t.Fatalf("unresolved note noise: %+v %v", report, err)
	}
}
