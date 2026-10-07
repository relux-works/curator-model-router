package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func evalFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "evalrun", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func evalInputs(t *testing.T) (EvalRun, EvalRunMapping, Registry) {
	t.Helper()
	var v EvalRun
	var m EvalRunMapping
	var r Registry
	for _, x := range []struct {
		name string
		out  any
	}{{"export.json", &v}, {"mapping.json", &m}, {"registry.json", &r}} {
		if err := Decode(evalFixture(t, x.name), x.out); err != nil {
			t.Fatal(err)
		}
	}
	return v, m, r
}
func evalError(t *testing.T, err error, want string) {
	t.Helper()
	var e *Error
	var c *canonical.Error
	if errors.As(err, &e) && e.Code == want || errors.As(err, &c) && c.Code == want {
		return
	}
	t.Fatalf("error=%v; want typed %s", err, want)
}
func evalDoc(t *testing.T, v EvalRun, m EvalRunMapping, r Registry) Import {
	t.Helper()
	d, _, err := ImportEvalRun(v, m, r)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func evalView(t *testing.T, docs []Import, r Registry, d Derivation, c Fingerprint) View {
	t.Helper()
	req := RequirementsInput{SchemaVersion: SchemaVersion, Project: "synthetic-project", Role: "reviewer", Requirements: []Requirement{{Category: "review.code", Weight: 10000}}, Mapping: CategoryMapping{SchemaVersion, "synthetic-requirements", "1", []RoleMapping{}, []RoleMapping{}}}
	view, err := Derive(snapshotFor(t, r, docs), docs, r, req, d, []Fingerprint{c}, "2026-10-02T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return view
}
func TestEvalRunTypedRefusalVectors(t *testing.T) {
	var f struct {
		SchemaVersion string `json:"schema_version"`
		Vectors       []struct {
			Name    string         `json:"name"`
			Export  EvalRun        `json:"export"`
			Mapping EvalRunMapping `json:"mapping"`
			Code    string         `json:"code"`
		} `json:"vectors"`
	}
	if err := Decode(evalFixture(t, "refusals.json"), &f); err != nil {
		t.Fatal(err)
	}
	_, _, r := evalInputs(t)
	for _, v := range f.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			raw, err := canonical.Marshal(v.Export)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = EvalRunImport(raw, v.Mapping, r)
			evalError(t, err, v.Code)
		})
	}
	for _, v := range []struct{ name, code string }{{"null.json", canonical.Null}, {"nonfinite.json", canonical.Nonfinite}, {"unknown-field.json", "evidence_unknown_field"}, {"duplicate-key.json", canonical.DuplicateKey}, {"trailing.json", canonical.InvalidJSON}} {
		t.Run(v.name, func(t *testing.T) {
			_, m, r := evalInputs(t)
			_, _, err := EvalRunImport(evalFixture(t, v.name), m, r)
			evalError(t, err, v.code)
		})
	}
}
func TestEvalRunNativeRoundTripAndRepeatStoreAdd(t *testing.T) {
	v, m, r := evalInputs(t)
	doc := evalDoc(t, v, m, r)
	raw, err := canonical.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, evalFixture(t, "import.canonical.json")) {
		t.Fatalf("import golden drift: %s", raw)
	}
	id, err := doc.Digest()
	if err != nil || id != strings.TrimSpace(string(evalFixture(t, "import.digest"))) {
		t.Fatalf("digest=%s error=%v", id, err)
	}
	native, _, err := Native(raw, r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := canonical.Marshal(native)
	if !bytes.Equal(b, raw) {
		t.Fatal("native changed bytes")
	}
	s, err := NewStore(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AddEvalRun(evalFixture(t, "export.json"), m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.AddEvalRun(evalFixture(t, "export.json"), m)
	if err != nil || !again.AlreadyPresent || again.ImportDigest != first.ImportDigest || again.SnapshotDigest != first.SnapshotDigest {
		t.Fatalf("repeat=%+v error=%v", again, err)
	}
	snap, sid, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	b, _ = canonical.Marshal(snap)
	if !bytes.Equal(b, evalFixture(t, "snapshot.canonical.json")) || sid != strings.TrimSpace(string(evalFixture(t, "snapshot.digest"))) {
		t.Fatalf("snapshot drift: %s %s", b, sid)
	}
	loaded, err := s.Load(snap)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("load: %v", err)
	}
	b, _ = canonical.Marshal(loaded[0])
	if !bytes.Equal(b, raw) {
		t.Fatal("store changed import")
	}
}
func TestEvalRunRelocatedReformattedExport(t *testing.T) {
	_, m, r := evalInputs(t)
	path := filepath.Join(t.TempDir(), "relocated.json")
	if err := os.WriteFile(path, evalFixture(t, "export-reformatted.json"), 0600); err != nil {
		t.Fatal(err)
	}
	relocated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := EvalRunImport(evalFixture(t, "export.json"), m, r)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := EvalRunImport(relocated, m, r)
	if err != nil {
		t.Fatal(err)
	}
	ad, _ := a.Digest()
	bd, _ := b.Digest()
	if ad != bd {
		t.Fatal("relocation/whitespace changed identity")
	}
}
func TestEvalRunChangedMappingUpdatedReferences(t *testing.T) {
	v, m, r := evalInputs(t)
	first := evalDoc(t, v, m, r)
	m.Version = "2"
	m.Models = append(m.Models, EvalRunNameMapping{"Unused Synthetic Model", "gpt-6.1-sol"})
	v.MappingRef.Version = m.Version
	v.MappingDigest, _ = m.Digest()
	second := evalDoc(t, v, m, r)
	a, _ := first.Digest()
	b, _ := second.Digest()
	if a == b || first.Importer.Version == second.Importer.Version {
		t.Fatal("mapping binding lost")
	}
	if !reflect.DeepEqual(first.Observations, second.Observations) {
		t.Fatal("unused mapping changed source records")
	}
	s, err := NewStore(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(first); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(second); err != nil {
		t.Fatal(err)
	}
	snap, _, docs, _, err := s.Inspect()
	if err != nil || len(snap.Imports) != 2 {
		t.Fatalf("original not preserved: %v", err)
	}
	c := first.Observations[0].Subject
	view := evalView(t, docs, r, ReviewDerivationV2(), c)
	if len(view.Results[0].Contributions) != 1 {
		t.Fatal("reprint contributed twice")
	}
}
func TestEvalRunExactEffortVectors(t *testing.T) {
	for _, token := range []string{"", "unknown", "none", "ultrahigh"} {
		t.Run("effort="+token, func(t *testing.T) {
			v, m, r := evalInputs(t)
			for i := range v.Results {
				v.Results[i].Subject.Effort = token
			}
			doc, report, err := ImportEvalRun(v, m, r)
			if err != nil {
				t.Fatal(err)
			}
			c := doc.Observations[0].Subject
			view := evalView(t, []Import{doc}, r, ReviewDerivationV2(), c)
			if c.Effort != token {
				t.Fatal("effort rewritten")
			}
			if token == "none" {
				if view.Results[0].Score == nil {
					t.Fatal("explicit none did not match")
				}
			} else if view.Results[0].Score != nil {
				t.Fatal("unknown effort matched, including both sides unknown")
			}
			found := false
			for _, i := range report.Issues {
				found = found || i.Code == "effort_unrecognised"
			}
			if (token == "ultrahigh") != found {
				t.Fatalf("diagnostics=%+v", report)
			}
		})
	}
}
func TestEvalRunUnresolvedNamesAndRuntimeDiagnosticsSurviveStore(t *testing.T) {
	for _, kind := range []string{"model-resembles-id", "unmapped-runtime", "mapped-model-absent", "unknown-category", "unknown-model-token", "unmapped-runtime-effort"} {
		t.Run(kind, func(t *testing.T) {
			v, m, r := evalInputs(t)
			switch kind {
			case "model-resembles-id":
				for i := range v.Results {
					v.Results[i].Subject.ModelName = "gpt-6.1-sol"
				}
			case "unmapped-runtime", "unmapped-runtime-effort":
				for i := range v.Results {
					v.Results[i].Subject.RuntimeName = "codex"
					if kind == "unmapped-runtime-effort" {
						v.Results[i].Subject.Effort = "ultrahigh"
					}
				}
			case "unknown-model-token":
				for i := range v.Results {
					v.Results[i].Subject.ModelName = "unknown"
				}
			case "mapped-model-absent":
				m.Models[0].InternalID = "not-registered"
				v.MappingDigest, _ = m.Digest()
			case "unknown-category":
				v.Benchmark.Categories = append(v.Benchmark.Categories, CategoryCoverage{"review.synthetic", "unknown category retained"})
				for i := range v.Results {
					v.Results[i].Categories = append(v.Results[i].Categories, "review.synthetic")
				}
			}
			raw, err := canonical.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewStore(t.TempDir(), r)
			if err != nil {
				t.Fatal(err)
			}
			added, err := s.AddEvalRun(raw, m)
			if err != nil {
				t.Fatal(err)
			}
			want := "model_unresolved"
			if kind == "unmapped-runtime" || kind == "unmapped-runtime-effort" {
				want = "runtime_unresolved"
			}
			if kind == "unknown-category" {
				want = "category_unknown"
			}
			found := false
			for _, i := range added.Report.Issues {
				found = found || i.Code == want
			}
			if !found {
				t.Fatalf("lost %s: %+v", want, added.Report)
			}
			if kind == "unmapped-runtime-effort" {
				found = false
				for _, issue := range added.Report.Issues {
					found = found || issue.Code == "effort_unrecognised"
				}
				if !found {
					t.Fatal("known model vocabulary diagnostic lost with unresolved runtime")
				}
			}
			_, _, docs, _, err := s.Inspect()
			if err != nil {
				t.Fatal(err)
			}
			if kind != "unknown-category" {
				for _, o := range docs[0].Observations {
					if o.SubjectResolution != "unresolved" {
						t.Fatal("unmapped name resolved")
					}
				}
				c := testCandidate(t)
				view := evalView(t, docs, r, ReviewDerivationV2(), c)
				if view.Results[0].Score != nil {
					t.Fatal("unresolved subject matched")
				}
			}
		})
	}
}
func TestEvalRunProtocolChangeSameVersionRefused(t *testing.T) {
	v, m, r := evalInputs(t)
	doc := evalDoc(t, v, m, r)
	s, err := NewStore(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(doc); err != nil {
		t.Fatal(err)
	}
	v.Benchmark.ProtocolDigest = "sha256:" + strings.Repeat("1", 64)
	changed := evalDoc(t, v, m, r)
	_, err = s.Add(changed)
	evalError(t, err, "evidence_benchmark_conflict")
	snap, _, err := s.Current()
	if err != nil || len(snap.Imports) != 1 {
		t.Fatal("refusal modified store")
	}
}
func TestEvalRunZeroVersusAbsentRow(t *testing.T) {
	v, m, r := evalInputs(t)
	for i := range v.Results {
		v.Results[i].Value = 0
	}
	doc := evalDoc(t, v, m, r)
	view := evalView(t, []Import{doc}, r, ReviewDerivationV2(), doc.Observations[0].Subject)
	if view.Results[0].Score == nil || *view.Results[0].Score != 0 || len(view.Results[0].Contributions) != 1 {
		t.Fatal("measured zero lost")
	}
	v.Results = []EvalRunResult{}
	empty := evalDoc(t, v, m, r)
	view = evalView(t, []Import{empty}, r, ReviewDerivationV2(), doc.Observations[0].Subject)
	if view.Results[0].Score != nil {
		t.Fatal("absent row became zero")
	}
}
func TestEvalRunObservationAndExportTimesDiffer(t *testing.T) {
	v, m, r := evalInputs(t)
	doc := evalDoc(t, v, m, r)
	if doc.ImportedAt != v.ExportedAt || doc.MeasuredBy != "internal" || doc.Source != "internal-evals" {
		t.Fatal("header facts lost")
	}
	for _, o := range doc.Observations {
		if o.ObservedAt != v.Results[0].ObservedAt || o.Provenance.RetrievedAt != v.ExportedAt || o.ObservedAt == o.Provenance.RetrievedAt || o.Provenance.Source != "internal-evals" || o.Provenance.OriginRef != "evalrun:synthetic-run:synthetic-result" || o.Provenance.RawArtifactRef != "artifact:synthetic-only" || o.EvidenceKind != "measured" || o.GradingMethod != "human" {
			t.Fatal("source/provenance times or semantics lost")
		}
		if o.Cost == nil || o.Cost.USD == nil || *o.Cost.USD != 0 || o.SampleCount == nil || *o.SampleCount != 10 {
			t.Fatal("per-sample costs/count lost")
		}
	}
}
func TestEvalRunReprintAndAppendOnlyCorrection(t *testing.T) {
	v, m, r := evalInputs(t)
	first := evalDoc(t, v, m, r)
	c := first.Observations[0].Subject
	v.ExportedAt = "2026-10-03T00:00:00Z"
	reprint := evalDoc(t, v, m, r)
	docs := []Import{first, reprint}
	view := evalView(t, docs, r, ReviewDerivationV2(), c)
	if len(view.Results[0].Contributions) != 1 {
		t.Fatal("reprint counted twice")
	}
	for i := range v.Results {
		for _, o := range first.Observations {
			if o.Metric == v.Results[i].Metric {
				v.Results[i].Supersedes = []string{o.ID}
			}
		}
		for _, o := range reprint.Observations {
			if o.Metric == v.Results[i].Metric {
				v.Results[i].Supersedes = append(v.Results[i].Supersedes, o.ID)
			}
		}
		canonical.SortByKey(v.Results[i].Supersedes, func(s string) string { return s })
		if v.Results[i].Metric == "review_f1" {
			v.Results[i].Value = 0.6
		}
	}
	corrected := evalDoc(t, v, m, r)
	docs = append(docs, corrected)
	active, err := Resolve(docs)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, rec := range active.Records {
		counts[rec.Status]++
	}
	if counts["active"] != 4 || counts["superseded"] != 8 {
		t.Fatalf("lifecycle=%v", counts)
	}
	view = evalView(t, docs, r, ReviewDerivationV2(), c)
	if view.Results[0].Score == nil || math.Abs(*view.Results[0].Score-0.6) > 1e-12 {
		t.Fatal("correction not used")
	}
	s, err := NewStore(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if _, err = s.Add(d); err != nil {
			t.Fatal(err)
		}
	}
	snap, _, err := s.Current()
	if err != nil || len(snap.Imports) != 3 {
		t.Fatal("append-only imports lost")
	}
}
func TestEvalRunPrepareDoesNotMutateCaller(t *testing.T) {
	v, m, r := evalInputs(t)
	before, _ := json.Marshal(v)
	_ = evalDoc(t, v, m, r)
	after, _ := json.Marshal(v)
	if !bytes.Equal(before, after) {
		t.Fatal("caller mutated")
	}
}

// exactProjection is an independent test oracle for the #13 seam, absent on
// this worktree. It verifies the numeric vectors from canonical W2 view facts.
func exactProjection(s Suitability, req RequirementsInput, d Derivation) (*int64, int64) {
	var f *int64
	if s.Score != nil {
		u := new(big.Rat)
		for _, c := range s.Contributions {
			r := new(big.Rat)
			r.SetString(string(mustNumber(c.Weight)))
			if c.Direction == "-" {
				u.Sub(u, r)
			} else {
				u.Add(u, r)
			}
		}
		u.Add(u, big.NewRat(1, 1))
		u.Mul(u, big.NewRat(5000, 1))
		x := roundEven(u)
		f = &x
	}
	total := new(big.Rat)
	sum := new(big.Rat)
	for i, c := range s.Coverage {
		w := new(big.Rat)
		w.SetString(string(mustNumber(req.Requirements[i].Weight)))
		sum.Add(sum, w)
		if c.Kind == "measured" {
			a := big.NewRat(10000, 1)
			if c.Match == "partial" {
				p := new(big.Rat)
				p.SetString(string(mustNumber(d.PartialDiscount)))
				a.Mul(a, p)
			}
			total.Add(total, new(big.Rat).Mul(w, a))
		}
	}
	if sum.Sign() == 0 {
		return f, 0
	}
	return f, roundEven(total.Quo(total, sum))
}
func mustNumber(v float64) []byte { b, _ := json.Marshal(v); return b }
func roundEven(r *big.Rat) int64 {
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	cmp := new(big.Int).Mul(rem, big.NewInt(2)).Cmp(r.Denom())
	if cmp > 0 || cmp == 0 && q.Bit(0) == 1 {
		q.Add(q, big.NewInt(1))
	}
	return q.Int64()
}

func TestEvalRunAbsentRuntimeIsPartialAndExactNames(t *testing.T) {
	for _, kind := range []string{"absent-runtime", "model-case", "runtime-case"} {
		t.Run(kind, func(t *testing.T) {
			v, m, r := evalInputs(t)
			for i := range v.Results {
				switch kind {
				case "absent-runtime":
					v.Results[i].Subject.RuntimeName = ""
				case "model-case":
					v.Results[i].Subject.ModelName = "synthetic model"
				case "runtime-case":
					v.Results[i].Subject.RuntimeName = "synthetic runtime"
				}
			}
			doc, report, err := ImportEvalRun(v, m, r)
			if err != nil {
				t.Fatal(err)
			}
			c := testCandidate(t)
			view := evalView(t, []Import{doc}, r, ReviewDerivationV2(), c)
			if kind == "absent-runtime" {
				if doc.Observations[0].Subject.Runtime != "" || view.Results[0].Score == nil || *view.Results[0].Score != 0.4 || view.Results[0].Coverage[0].Match != "partial" || len(report.Issues) != 0 {
					t.Fatal("absent runtime not retained as unknown partial")
				}
			} else if view.Results[0].Score != nil {
				t.Fatal("source name was case folded")
			}
		})
	}
}
func TestEvalRunMappingStrictBytes(t *testing.T) {
	original := evalFixture(t, "mapping.json")
	for _, v := range []struct {
		name string
		raw  []byte
		code string
	}{
		{"NullCollection", bytes.Replace(original, []byte(`"models":[`), []byte(`"models":null,"unused":[`), 1), canonical.Null},
		{"UnknownField", bytes.Replace(original, []byte(`"name":`), []byte(`"unexpected":1,"name":`), 1), "evidence_unknown_field"},
		{"MissingCollection", []byte(`{"schema_version":"evalrun-mapping-v1","name":"test","version":"1","models":[]}`), "evidence_missing_field"},
	} {
		t.Run(v.name, func(t *testing.T) { var m EvalRunMapping; err := Decode(v.raw, &m); evalError(t, err, v.code) })
	}
}
