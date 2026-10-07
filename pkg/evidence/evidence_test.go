package evidence

import (
	"errors"
	"github.com/relux-works/curator-model-router/pkg/canonical"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T, name string, out any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err = Decode(b, out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}
func testRegistry(t *testing.T) Registry {
	t.Helper()
	var r Registry
	fixture(t, "registry.json", &r)
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	return r
}
func testCandidate(t *testing.T) Fingerprint {
	t.Helper()
	var v struct {
		SchemaVersion string        `json:"schema_version"`
		Candidates    []Fingerprint `json:"candidates"`
	}
	fixture(t, "candidate.json", &v)
	return v.Candidates[0]
}
func testObservation(t *testing.T) (Observation, Benchmark) {
	t.Helper()
	var v struct {
		SchemaVersion string      `json:"schema_version"`
		Observation   Observation `json:"observation"`
		Benchmark     Benchmark   `json:"benchmark"`
	}
	fixture(t, "observation.json", &v)
	return v.Observation, v.Benchmark
}
func testNote(t *testing.T) Note {
	t.Helper()
	var v struct {
		SchemaVersion string `json:"schema_version"`
		Note          Note   `json:"note"`
	}
	fixture(t, "note.json", &v)
	return v.Note
}
func codeIs(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error=%v, want typed %s", err, code)
	}
}
func snapshotFor(t *testing.T, r Registry, docs []Import) Snapshot {
	t.Helper()
	ids := []string{}
	for _, d := range docs {
		id, err := d.Digest()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	canonical.SortByKey(ids, func(s string) string { return s })
	rd, err := canonical.Digest(r)
	if err != nil {
		t.Fatal(err)
	}
	return Snapshot{SchemaVersion, "evidence-snapshot", ids, "v1", r.Reference, rd}
}
func TestAcceptance1CanonicalRoundTrip(t *testing.T) {
	r := testRegistry(t)
	raw, err := os.ReadFile("testdata/roundtrip.input.json")
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := Native(raw, r)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/roundtrip.canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := os.ReadFile("testdata/roundtrip.digest")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		b, err := canonical.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != string(expected) {
			t.Fatalf("canonical drift: %s", b)
		}
		id, err := v.Digest()
		if err != nil {
			t.Fatal(err)
		}
		if id != strings.TrimSpace(string(wantDigest)) {
			t.Fatalf("digest=%s", id)
		}
		v, _, err = Native(b, r)
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewStore(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(v)
	if err != nil {
		t.Fatal(err)
	}
	snap, sid, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	if sid != added.SnapshotDigest || len(snap.Imports) != 1 || snap.Imports[0] != added.ImportDigest {
		t.Fatal("snapshot lost import")
	}
	loaded, err := s.Load(snap)
	if err != nil {
		t.Fatal(err)
	}
	id, err := loaded[0].Digest()
	if err != nil || id != added.ImportDigest {
		t.Fatalf("round trip changed stored import: %s %v", id, err)
	}
}
func TestAcceptance3EffortExactness(t *testing.T) {
	r := testRegistry(t)
	candidate := testCandidate(t)
	for _, effort := range []string{"", canonical.Unknown, "ultrahigh", "max"} {
		t.Run("effort="+effort, func(t *testing.T) {
			o, b := testObservation(t)
			o.ID = ""
			o.Subject.Effort = effort
			v := EmptyImport("fixture", Identity{"native", "1"}, "2026-10-01T00:00:00Z")
			v.Benchmarks = []Benchmark{b}
			v.Observations = []Observation{o}
			doc, report, err := Prepare(v, r)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Observations[0].Subject.Effort != effort {
				t.Fatal("effort rewritten")
			}
			if ok, _ := Match(doc.Observations[0].Subject, candidate, r); ok {
				t.Fatal("unknown or different effort matched")
			}
			if effort == "ultrahigh" && (len(report.Issues) != 1 || report.Issues[0].Code != "effort_unrecognised") {
				t.Fatalf("unknown effort not flagged: %+v", report)
			}
		})
	}
	for _, tc := range []struct {
		name        string
		transfer    *Transfer
		uncertainty *Uncertainty
		code        string
	}{
		{"missing-transfer", nil, nil, "evidence_transfer_required"},
		{"missing-rule", &Transfer{From: TransferFrom{Effort: "max"}}, &Uncertainty{"range", 1}, "evidence_transfer_required"},
		{"missing-uncertainty", &Transfer{From: TransferFrom{Effort: "max"}, RuleID: "effort-transfer", RuleVersion: "1"}, nil, "evidence_transfer_required"},
		{"declared-transfer", &Transfer{From: TransferFrom{Effort: "max"}, RuleID: "effort-transfer", RuleVersion: "1"}, &Uncertainty{"range", 1}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, b := testObservation(t)
			o.ID = ""
			o.EvidenceKind = "transferred"
			o.Transfer = tc.transfer
			o.Uncertainty = tc.uncertainty
			v := EmptyImport("fixture", Identity{"native", "1"}, "2026-10-01T00:00:00Z")
			v.Benchmarks = []Benchmark{b}
			v.Observations = []Observation{o}
			_, _, err := Prepare(v, r)
			if tc.code != "" {
				codeIs(t, err, tc.code)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestAcceptance4UnresolvedModels(t *testing.T) {
	r := testRegistry(t)
	o, b := testObservation(t)
	o.ID = ""
	o.Subject.ModelID = "unmapped-model"
	o.Subject.Effort = "high"
	v := EmptyImport("fixture", Identity{"native", "1"}, "2026-10-01T00:00:00Z")
	v.Benchmarks = []Benchmark{b}
	v.Observations = []Observation{o}
	doc, report, err := Prepare(v, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Observations) != 1 || doc.Observations[0].SubjectResolution != o.SubjectResolution {
		t.Fatal("unresolved record dropped")
	}
	found := false
	for _, i := range report.Issues {
		if i.Code == "model_unresolved" {
			found = true
		}
	}
	if !found {
		t.Fatal("unresolved not reported")
	}
	candidate := testCandidate(t)
	candidate.ModelID = "unmapped-model"
	if ok, _ := Match(doc.Observations[0].Subject, candidate, r); ok {
		t.Fatal("unresolved model matched")
	}
	s, err := NewStore(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(doc); err != nil {
		t.Fatal(err)
	}
	unresolved, err := s.Unresolved()
	if err != nil || len(unresolved.Issues) != 1 {
		t.Fatalf("unresolved store report: %+v %v", unresolved, err)
	}
}

type lifecycleVector struct {
	Name        string       `json:"name"`
	Notes       []Note       `json:"notes"`
	Retractions []Retraction `json:"retractions"`
	Active      []string     `json:"active"`
	Conflict    bool         `json:"conflict"`
	Error       string       `json:"error"`
}

func permutations[T any](items []T) [][]T {
	out := [][]T{}
	var walk func(int)
	walk = func(i int) {
		if i == len(items) {
			out = append(out, append([]T{}, items...))
			return
		}
		for j := i; j < len(items); j++ {
			items[i], items[j] = items[j], items[i]
			walk(i + 1)
			items[i], items[j] = items[j], items[i]
		}
	}
	walk(0)
	return out
}
func TestAcceptance5RecordLifecycle(t *testing.T) {
	var data struct {
		SchemaVersion string            `json:"schema_version"`
		Vectors       []lifecycleVector `json:"vectors"`
	}
	fixture(t, "lifecycle.json", &data)
	r := testRegistry(t)
	for _, v := range data.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			docs := []Import{}
			for _, n := range v.Notes {
				doc := EmptyImport("fixture", Identity{"notes", "1"}, "2026-10-01T00:00:00Z")
				doc.Notes = []Note{n}
				docs = append(docs, doc)
			}
			for _, ret := range v.Retractions {
				doc := EmptyImport("fixture", Identity{"notes", "1"}, "2026-10-01T00:00:00Z")
				doc.Retractions = []Retraction{ret}
				docs = append(docs, doc)
			}
			if v.Error != "" {
				combined := EmptyImport("fixture", Identity{"notes", "1"}, "2026-10-01T00:00:00Z")
				combined.Notes = v.Notes
				store, err := NewStore(t.TempDir(), r)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.Add(combined)
				codeIs(t, err, v.Error)
				return
			}
			orders := permutations(docs)
			var snapshotID string
			for permutation, order := range orders {
				a, err := Resolve(order)
				if err != nil {
					t.Fatal(err)
				}
				active := []string{}
				for _, rec := range a.Records {
					if rec.Status == "active" && rec.Note != nil {
						active = append(active, rec.Note.Claim.Statement)
					}
				}
				canonical.SortByKey(active, func(s string) string { return s })
				if strings.Join(active, ",") != strings.Join(v.Active, ",") {
					t.Fatalf("active=%v, want %v", active, v.Active)
				}
				if v.Conflict {
					if len(a.Issues) != 1 || a.Issues[0].Code != "evidence_conflict" || a.Issues[0].Ref != v.Notes[1].Supersedes[0] {
						t.Fatalf("conflict=%v", a.Issues)
					}
				} else if len(a.Issues) != 0 {
					t.Fatalf("unexpected issues=%v", a.Issues)
				}
				// All orders exercise the graph; two opposite orders also exercise persistence.
				if permutation != 0 && permutation != len(orders)-1 {
					continue
				}
				store, err := NewStore(t.TempDir(), r)
				if err != nil {
					t.Fatal(err)
				}
				for _, doc := range order {
					if _, err = store.Add(doc); err != nil {
						t.Fatal(err)
					}
				}
				_, currentID, loaded, stored, err := store.Inspect()
				if snapshotID != "" && snapshotID != currentID {
					t.Fatal("snapshot identity depends on import order")
				}
				snapshotID = currentID
				if err != nil {
					t.Fatal(err)
				}
				if len(loaded) != len(docs) {
					t.Fatal("append-only imports changed")
				}
				if len(stored.Records) != len(a.Records) {
					t.Fatal("stored records dropped")
				}
				for i := range stored.Records {
					if stored.Records[i].ID != a.Records[i].ID || stored.Records[i].Status != a.Records[i].Status {
						t.Fatal("status depends on import order")
					}
				}
			}
		})
	}
}

type derivationVector struct {
	Name         string            `json:"name"`
	Imports      []Import          `json:"imports"`
	Requirements RequirementsInput `json:"requirements"`
	EvaluatedAt  string            `json:"evaluated_at"`
	Inputs       ViewInputs        `json:"inputs"`
	ViewID       string            `json:"view_id"`
	Score        *float64          `json:"score,omitempty"`
	Grade        string            `json:"grade"`
	Coverage     string            `json:"coverage"`
	Match        string            `json:"match"`
}

func TestAcceptance6DerivationVectors(t *testing.T) {
	var data struct {
		SchemaVersion string             `json:"schema_version"`
		Vectors       []derivationVector `json:"vectors"`
	}
	fixture(t, "derivation.json", &data)
	r := testRegistry(t)
	candidate := testCandidate(t)
	ids := map[string]string{}
	for _, v := range data.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			snapshot := snapshotFor(t, r, v.Imports)
			view, err := Derive(snapshot, v.Imports, r, v.Requirements, DefaultDerivation(), []Fingerprint{candidate}, v.EvaluatedAt)
			if err != nil {
				t.Fatal(err)
			}
			if err = view.Validate(); err != nil {
				t.Fatal(err)
			}
			if view.ID != v.ViewID || view.Inputs != v.Inputs {
				t.Fatalf("view identity drift: got=%+v want=%+v", view.Inputs, v.Inputs)
			}
			ids[v.Name] = view.ID
			s := view.Results[0]
			if s.Grade != v.Grade || s.Coverage[0].Kind != v.Coverage || s.Coverage[0].Match != v.Match {
				t.Fatalf("result=%+v", s)
			}
			if (s.Score == nil) != (v.Score == nil) {
				t.Fatal("unknown became zero")
			}
			if s.Score != nil && math.Abs(*s.Score-*v.Score) > 1e-12 {
				t.Fatalf("score=%g", *s.Score)
			}
			total := 0.0
			for _, c := range s.Contributions {
				if c.Direction == "-" {
					total -= c.Weight
				} else {
					total += c.Weight
				}
				if v.Name == "stale-note" && (!c.Stale || c.Weight != 0 || c.AgeS == nil) {
					t.Fatal("stale note has nonzero or unexplained weight")
				}
			}
			if s.Score != nil && math.Abs(total-*s.Score) > 1e-12 {
				t.Fatal("contributions disagree with score")
			}
			again, err := Derive(snapshot, v.Imports, r, v.Requirements, DefaultDerivation(), []Fingerprint{candidate}, v.EvaluatedAt)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := canonical.Marshal(view)
			b, _ := canonical.Marshal(again)
			if string(a) != string(b) {
				t.Fatal("replay changed view")
			}
			store, err := NewStore(t.TempDir(), r)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.SaveView(view); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.ReadView(view.ID)
			if err != nil {
				t.Fatal(err)
			}
			b, err = canonical.Marshal(loaded)
			if err != nil || string(a) != string(b) {
				t.Fatal("view round trip changed")
			}
		})
	}
	if ids["notes-only"] == ids["project-two"] || ids["notes-only"] == ids["stale-note"] {
		t.Fatal("different project or time reused view")
	}
}
func TestAcceptance7HermeticConcurrentStore(t *testing.T) {
	root := t.TempDir()
	r := testRegistry(t)
	s, err := NewStore(root, r)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := NoteImport(testNote(t), "2026-10-01T00:00:00Z", r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(doc); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "current.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Add(doc)
	codeIs(t, err, "evidence_locked")
	if _, _, _, _, err = s.Inspect(); err != nil {
		t.Fatal("reader must not require lock:", err)
	}
	if err = os.Remove(filepath.Join(root, "current.lock")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for read := 0; read < 40; read++ {
				select {
				case <-done:
					return
				default:
					if _, _, _, _, err := s.Inspect(); err != nil {
						failures <- err
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 12; i++ {
		n := testNote(t)
		n.ID = ""
		n.Claim.Statement = "append " + strings.Repeat("x", i+1)
		v, _, err := NoteImport(n, "2026-10-01T00:00:00Z", r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	// Simultaneous writers either publish or return the stable locked refusal.
	start := make(chan struct{})
	codes := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := s.Add(doc); codes <- err }()
	}
	close(start)
	wg.Wait()
	close(codes)
	for err := range codes {
		if err != nil {
			codeIs(t, err, "evidence_locked")
		}
	}
}
func TestNativeStrictValidationAndUnknownCategory(t *testing.T) {
	r := testRegistry(t)
	o, b := testObservation(t)
	o.ID = ""
	o.Categories = []string{"new.category"}
	b.Categories = []CategoryCoverage{{"new.category", "unrecognised source category"}}
	v := EmptyImport("fixture", Identity{"native", "1"}, "2026-10-01T00:00:00Z")
	v.Benchmarks = []Benchmark{b}
	v.Observations = []Observation{o}
	doc, report, err := Prepare(v, r)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Observations[0].Categories[0] != "new.category" {
		t.Fatal("unknown category dropped")
	}
	if len(report.Issues) != 2 {
		t.Fatalf("unknown categories=%+v", report)
	}
	for _, tc := range []struct{ name, raw, code string }{
		{"status", "{\"schema_version\":\"evidence-v1\",\"status\":\"active\"}", "evidence_unknown_field"},
		{"case", "{\"schema_version\":\"evidence-v1\",\"Source\":\"fixture\"}", "evidence_unknown_field"},
		{"null", "{\"schema_version\":\"evidence-v1\",\"notes\":null}", canonical.Null},
		{"duplicate", "{\"schema_version\":\"evidence-v1\",\"source\":\"one\",\"source\":\"two\"}", canonical.DuplicateKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Native([]byte(tc.raw), r)
			var e *Error
			var c *canonical.Error
			code := ""
			if errors.As(err, &e) {
				code = e.Code
			}
			if errors.As(err, &c) {
				code = c.Code
			}
			if code != tc.code {
				t.Fatalf("error=%v want=%s", err, tc.code)
			}
		})
	}
}
func TestFingerprintMembers(t *testing.T) {
	r := testRegistry(t)
	candidate := testCandidate(t)
	for _, tc := range []struct {
		name   string
		mutate func(*Fingerprint)
		ok     bool
		match  string
	}{
		{"direct", func(f *Fingerprint) {}, true, "direct"},
		{"unknown-harness", func(f *Fingerprint) { f.HarnessVersion = "" }, true, "partial"},
		{"other-harness", func(f *Fingerprint) { f.HarnessVersion = "2.0" }, false, "none"},
		{"other-runtime", func(f *Fingerprint) { f.Runtime = "other" }, false, "none"},
		{"other-tool", func(f *Fingerprint) { f.ToolProfile = "other" }, false, "none"},
		{"other-execution", func(f *Fingerprint) { f.ExecutionProfile = "other" }, false, "none"},
		{"unknown-engine", func(f *Fingerprint) { f.EngineProfile = nil }, true, "partial"},
		{"other-engine-kv", func(f *Fingerprint) { v := int64(8192); f.EngineProfile.KVContextTokens = &v }, false, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := testCandidate(t)
			tc.mutate(&f)
			ok, match := Match(f, candidate, r)
			if ok != tc.ok || match != tc.match {
				t.Fatalf("got %v %s", ok, match)
			}
		})
	}
}

func TestNoteStalenessAtFrozenTime(t *testing.T) {
	r := testRegistry(t)
	candidate := testCandidate(t)
	n := testNote(t)
	doc, _, err := NoteImport(n, "2026-10-01T00:00:00Z", r)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := RoleRequirements("reviewer", "fixture")
	req.Requirements = []Requirement{{Category: "review.code", Weight: 1}}
	snapshot := snapshotFor(t, r, []Import{doc})
	for _, tc := range []struct {
		at    string
		stale bool
	}{
		{"2026-11-01T00:00:00Z", false}, {"2026-11-01T23:59:59Z", false}, {"2026-11-02T00:00:00Z", true},
	} {
		t.Run(tc.at, func(t *testing.T) {
			v, err := Derive(snapshot, []Import{doc}, r, req, DefaultDerivation(), []Fingerprint{candidate}, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			c := v.Results[0].Contributions[0]
			if c.Stale != tc.stale || tc.stale && c.Weight != 0 {
				t.Fatalf("staleness=%+v", c)
			}
		})
	}
}
func TestViewSixInputsAndIntegrity(t *testing.T) {
	r := testRegistry(t)
	candidate := testCandidate(t)
	req, _ := RoleRequirements("reviewer", "fixture")
	d := DefaultDerivation()
	n := testNote(t)
	doc, _, err := NoteImport(n, "2026-10-01T00:00:00Z", r)
	if err != nil {
		t.Fatal(err)
	}
	snap := snapshotFor(t, r, []Import{doc})
	view, err := Derive(snap, []Import{doc}, r, req, d, []Fingerprint{candidate}, "2026-10-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ViewInputs)
	}{
		{"snapshot", func(v *ViewInputs) { v.EvidenceSnapshotDigest = "sha256:" + strings.Repeat("1", 64) }},
		{"derivation", func(v *ViewInputs) { v.DerivationDigest = "sha256:" + strings.Repeat("1", 64) }},
		{"requirements", func(v *ViewInputs) { v.RoleRequirementsDigest = "sha256:" + strings.Repeat("1", 64) }},
		{"weights", func(v *ViewInputs) { v.WeightsPolicyDigest = "sha256:" + strings.Repeat("1", 64) }},
		{"time", func(v *ViewInputs) { v.EvaluatedAt = "2026-10-02T00:00:00Z" }},
		{"candidates", func(v *ViewInputs) { v.CandidateSetDigest = "sha256:" + strings.Repeat("1", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := view
			tc.mutate(&changed.Inputs)
			id, err := canonical.Digest(changed.Inputs)
			if err != nil {
				t.Fatal(err)
			}
			if id == view.ID {
				t.Fatal("view id did not bind changed input")
			}
			codeIs(t, changed.Validate(), "evidence_view_mismatch")
		})
	}
	// A caller cannot replay with a substituted import set or registry.
	_, err = Derive(snap, []Import{}, r, req, d, []Fingerprint{candidate}, "2026-10-01T00:00:00Z")
	codeIs(t, err, "evidence_snapshot_mismatch")
	other := r
	other.Reference.Version = "2"
	_, err = Derive(snap, []Import{doc}, other, req, d, []Fingerprint{candidate}, "2026-10-01T00:00:00Z")
	codeIs(t, err, "evidence_registry_mismatch")
}
func TestReprintsCountOnce(t *testing.T) {
	r := testRegistry(t)
	o, b := testObservation(t)
	o.ID = ""
	o.Subject = testCandidate(t)
	reprint := o
	reprint.Subject = Fingerprint{ModelID: o.Subject.ModelID, Effort: o.Subject.Effort}
	reprint.Provenance.RawArtifactRef = "artifact:reprint"
	doc := EmptyImport("fixture", Identity{"native", "1"}, "2026-10-01T00:00:00Z")
	doc.Benchmarks = []Benchmark{b}
	doc.Observations = []Observation{o, reprint}
	doc, _, err := Prepare(doc, r)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := RoleRequirements("developer", "fixture")
	req.Requirements = []Requirement{{Category: "code.fix", Weight: 1}}
	v, err := Derive(snapshotFor(t, r, []Import{doc}), []Import{doc}, r, req, DefaultDerivation(), []Fingerprint{testCandidate(t)}, "2026-10-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	s := v.Results[0]
	if len(s.Contributions) != 1 || s.Score == nil || math.Abs(*s.Score-0.4) > 1e-12 || s.Coverage[0].Match != "direct" {
		t.Fatalf("reprint counted twice or selected partial: %+v", s)
	}
}
func TestStoreImmutabilityAndCorruption(t *testing.T) {
	r := testRegistry(t)
	root := t.TempDir()
	s, err := NewStore(root, r)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := NoteImport(testNote(t), "2026-10-01T00:00:00Z", r)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Add(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "imports", first.ImportDigest+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Add(doc)
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotDigest != second.SnapshotDigest {
		t.Fatal("duplicate import changed snapshot")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("immutable import changed")
	}
	n := testNote(t)
	n.ID = ""
	n.Claim.Statement = "new independent note"
	newDoc, _, err := NoteImport(n, "2026-10-01T00:00:00Z", r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(newDoc); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("later import rewrote an earlier import")
	}
	changed := strings.Replace(string(before), "operator-judgement", "review-outcomes", 1)
	if err = os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = s.Inspect()
	codeIs(t, err, "evidence_store_corrupt")
	_, err = NewStore("", r)
	codeIs(t, err, "evidence_store_root")
}
func TestRetractionsCannotBeRetracted(t *testing.T) {
	r := testRegistry(t)
	ret := Retraction{Target: "ret:" + strings.Repeat("1", 64), Reason: "invalid restoration", Author: Author{"human", "operator"}, At: "2026-10-01T00:00:00Z"}
	_, _, err := RetractionImport(ret, "2026-10-01T00:00:00Z", r)
	codeIs(t, err, "evidence_invalid_target")
}

func TestAdvisoryCandidatesWithoutMeasurements(t *testing.T) {
	r := testRegistry(t)
	candidates := EvidenceCandidates([]Import{}, r)
	if len(candidates) != 2 {
		t.Fatal("empty store lost advisory unknown candidates")
	}
	req, _ := RoleRequirements("reviewer", "fixture")
	v, err := Derive(snapshotFor(t, r, []Import{}), []Import{}, r, req, DefaultDerivation(), candidates, "2026-10-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range v.Results {
		if s.Grade != "unknown" || s.Score != nil {
			t.Fatal("empty evidence invented a score")
		}
	}
	n := testNote(t)
	n.ID = ""
	n.Subject.Efforts = nil
	doc, _, err := NoteImport(n, "2026-10-01T00:00:00Z", r)
	if err != nil {
		t.Fatal(err)
	}
	candidates = EvidenceCandidates([]Import{doc}, r)
	if len(candidates) != 2 {
		t.Fatal("broad note dropped known effort candidates")
	}
	for _, c := range candidates {
		if !r.Accepts(c.ModelID, c.Effort) {
			t.Fatal("candidate assumed an unrecognised effort")
		}
	}
}

func TestProgrammaticCanonicalRefusals(t *testing.T) {
	r := testRegistry(t)
	for _, tc := range []struct {
		name   string
		mutate func(*Import)
		code   string
	}{
		{"invalid-utf8", func(v *Import) { v.Notes[0].Claim.Statement = string([]byte{0xff}) }, canonical.InvalidJSON},
		{"nonfinite", func(v *Import) {
			o, b := testObservation(t)
			o.ID = ""
			o.Value = math.Inf(1)
			v.Benchmarks = []Benchmark{b}
			v.Observations = []Observation{o}
		}, canonical.Nonfinite},
		{"null-required-collection", func(v *Import) { v.Retractions = nil }, canonical.Null},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := EmptyImport("fixture", Identity{"native", "1"}, "2026-10-01T00:00:00Z")
			n := testNote(t)
			n.ID = ""
			v.Notes = []Note{n}
			tc.mutate(&v)
			_, _, err := Prepare(v, r)
			var e *canonical.Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("error=%v want canonical %s", err, tc.code)
			}
		})
	}
	n := testNote(t)
	n.Claim.Statement = string([]byte{0xff})
	_, err := RecordAddress("note:", n)
	var e *canonical.Error
	if !errors.As(err, &e) || e.Code != canonical.InvalidJSON {
		t.Fatalf("record encoder lost malformed UTF-8: %v", err)
	}
}
