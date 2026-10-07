package evidence

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

var updateEstimator = os.Getenv("UPDATE_ESTIMATOR_GOLDENS") == "1"

func estBP(n int64) *int64 { return &n }

func estimatorNote() Note {
	return Note{Subject: NoteSubject{ModelID: "model-a", Runtime: "runtime-a", Efforts: []string{"high"}, HarnessVersionRange: "[1,2]"}, Claim: Claim{Polarity: "strength", Statement: "Synthetic prior", Categories: []string{"review.code"}}, Basis: "operator-judgement", Confidence: "high", EvidenceRefs: []EvidenceRef{}, Author: Author{"human", "synthetic-author"}, CreatedAt: "2026-09-01T00:00:00Z", ReviewBy: "2026-11-01"}
}

func estimatorFixture(t *testing.T, mutate func(*EstimatorInput)) EstimatorInput {
	t.Helper()
	r := Registry{SchemaVersion, Identity{"synthetic-registry", "1"}, []RegistryModel{{"model-a", []string{"high", "max", "none"}}}}
	engine := &routing.EngineProfile{Name: "synthetic-engine", EngineKind: "synthetic", WeightDigest: "sha256:" + strings.Repeat("a", 64), Quantization: "synthetic", KVContextTokens: estBP(4096), PrefillChunkTokens: estBP(512)}
	f := Fingerprint{ModelID: "model-a", Runtime: "runtime-a", HarnessVersion: "1.5", Effort: "high", EngineProfile: engine, ToolProfile: "tools-a", ExecutionProfile: "public-execution"}
	c := routing.ExecutionCandidate{ID: "a", RuntimeBindingID: "home-a", Runtime: f.Runtime, Model: f.ModelID, Effort: f.Effort, EngineProfile: estimatorCopy(engine), ToolProfile: f.ToolProfile, ExecutionMode: "batch", ContextProfile: routing.ContextProfile{Name: "synthetic-context", LockSHA256: canonical.Unknown}, Transport: "synthetic", ProviderID: "synthetic-provider", BillingClass: routing.BillingMetered}
	doc := EmptyImport("synthetic-estimator", Identity{"native", "1"}, "2026-10-01T00:00:00Z")
	doc.Benchmarks = []Benchmark{{ID: "synthetic-bench", Version: "1", Publisher: "synthetic", Categories: []CategoryCoverage{{"review.code", "synthetic"}}, Metrics: []Metric{{Name: "utility", Unit: "ratio", Direction: "higher_better", Scale: &Scale{0, 1}, Description: "Synthetic utility"}}, GradingMethod: "tests"}}
	doc.Observations = []Observation{{BenchmarkRef: Reference{"synthetic-bench", "1"}, Subject: estimatorCopy(f), SubjectResolution: "resolved", Categories: []string{"review.code"}, Metric: "utility", Value: 0.8, EvidenceKind: "measured", GradingMethod: "tests", ObservedAt: "2026-10-01T00:00:00Z", Provenance: Provenance{"synthetic", "2026-10-01T00:00:00Z", "unknown", "synthetic-origin"}}}
	d := DefaultDerivation()
	d.Normalisations = []Normalisation{{Reference{"synthetic-bench", "1"}, "utility", 0, 1, "higher_better", []string{"review.code"}}}
	input := EstimatorInput{SchemaVersion: EstimatorInputSchemaVersion, Imports: []Import{doc}, Registry: r, Requirements: RequirementsInput{SchemaVersion: SchemaVersion, Project: "synthetic-project", Role: "reviewer", Requirements: []Requirement{{Category: "review.code", Weight: 10000}}, Mapping: CategoryMapping{SchemaVersion, "synthetic-mapping", "1", []RoleMapping{}, []RoleMapping{}}}, Derivation: d, EvaluatedAt: "2026-10-02T00:00:00Z", Candidates: []routing.ExecutionCandidate{c}, Fingerprints: []CandidateFingerprint{{"a", f}}, Config: DefaultEstimatorConfig()}
	if mutate != nil {
		mutate(&input)
	}
	for i, doc := range input.Imports {
		prepared, _, err := Prepare(doc, input.Registry)
		if err != nil {
			t.Fatal(err)
		}
		input.Imports[i] = prepared
	}
	sort.Slice(input.Imports, func(i, j int) bool {
		a, _ := input.Imports[i].Digest()
		b, _ := input.Imports[j].Digest()
		return a < b
	})
	input.Snapshot = snapshotFor(t, input.Registry, input.Imports)
	return input
}

func estimatorRefs(input EstimatorInput) routing.EvidenceSnapshotRef {
	ids := []string{}
	for _, doc := range input.Imports {
		for _, o := range doc.Observations {
			ids = append(ids, o.ID)
		}
		for _, n := range doc.Notes {
			ids = append(ids, n.ID)
		}
	}
	sort.Strings(ids)
	sid, _ := input.Snapshot.Digest()
	return routing.EvidenceSnapshotRef{Digest: sid, RecordIDs: ids}
}

func estimatorOutput(t *testing.T, input EstimatorInput) (*ViewEstimator, []routing.Estimate, routing.EstimatorPartition) {
	t.Helper()
	e, err := NewFitnessEstimator(input)
	if err != nil {
		t.Fatal(err)
	}
	estimates, err := e.Estimate(e.Requirements(), input.Candidates, estimatorRefs(input))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Partition(estimates)
	if err != nil {
		t.Fatal(err)
	}
	return e, estimates, p
}

func estimatorPrior(input *EstimatorInput) {
	input.Imports[0].Observations = []Observation{}
	input.Imports[0].Notes = []Note{estimatorNote()}
}
func estimatorPartial(input *EstimatorInput) { input.Fingerprints[0].Fingerprint.HarnessVersion = "" }
func estimatorTransfer(input *EstimatorInput) {
	o := &input.Imports[0].Observations[0]
	o.EvidenceKind = "transferred"
	o.Transfer = &Transfer{From: TransferFrom{ModelID: "source-model"}, RuleID: "synthetic-rule", RuleVersion: "1"}
	o.Uncertainty = &Uncertainty{Kind: "range", Value: 0.1}
}
func estimatorMissing(input *EstimatorInput, weight float64) {
	input.Requirements.Requirements[0].Weight = weight
	input.Requirements.Requirements = append(input.Requirements.Requirements, Requirement{Category: "review.spec", Weight: 10000 - weight})
}

func TestEstimatorAcceptance81(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*EstimatorInput)
		fitness  *int64
		coverage int64
		refs     int
	}{
		{"DirectMeasured", nil, estBP(9000), 10000, 1},
		{"PartialMeasured", estimatorPartial, estBP(7000), 5000, 1},
		{"DirectInterpolated", func(i *EstimatorInput) { i.Imports[0].Observations[0].EvidenceKind = "interpolated" }, estBP(9000), 7500, 1},
		{"DirectTransferred", estimatorTransfer, estBP(9000), 5000, 1},
		{"PartialTransferred", func(i *EstimatorInput) { estimatorTransfer(i); estimatorPartial(i) }, estBP(7000), 2500, 1},
		{"DirectMeasuredZero", func(i *EstimatorInput) { i.Imports[0].Observations[0].Value = 0 }, estBP(5000), 10000, 1},
		{"ScopedNoteIgnoredAxesUnknown", func(i *EstimatorInput) {
			estimatorPrior(i)
			f := &i.Fingerprints[0].Fingerprint
			f.EngineProfile = nil
			f.ToolProfile = ""
			f.ExecutionProfile = ""
		}, estBP(8000), 0, 1},
		{"UnscopedNote", func(i *EstimatorInput) {
			estimatorPrior(i)
			i.Imports[0].Notes[0].Subject = NoteSubject{ModelID: "model-a"}
		}, estBP(6500), 0, 1},
		{"EffortUnscopedNote", func(i *EstimatorInput) { estimatorPrior(i); i.Imports[0].Notes[0].Subject.Efforts = nil }, estBP(6500), 0, 1},
		{"ScopedNoteExcludesEffort", func(i *EstimatorInput) { estimatorPrior(i); i.Imports[0].Notes[0].Subject.Efforts = []string{"max"} }, nil, 0, 0},
		{"UnscopedNoteUnknownCandidateEffort", func(i *EstimatorInput) {
			estimatorPrior(i)
			i.Imports[0].Notes[0].Subject = NoteSubject{ModelID: "model-a"}
			i.Candidates[0].Effort = canonical.Unknown
			i.Fingerprints[0].Fingerprint.Effort = ""
		}, nil, 0, 0},
		{"HighStrengthPartial", func(i *EstimatorInput) { estimatorPrior(i); estimatorPartial(i) }, estBP(6500), 0, 1},
		{"MediumStrengthPartial", func(i *EstimatorInput) {
			estimatorPrior(i)
			estimatorPartial(i)
			i.Imports[0].Notes[0].Confidence = "medium"
		}, estBP(5900), 0, 1},
		{"HighWeaknessPartial", func(i *EstimatorInput) {
			estimatorPrior(i)
			estimatorPartial(i)
			i.Imports[0].Notes[0].Claim.Polarity = "weakness"
		}, estBP(3500), 0, 1},
		{"StaleNotesOnly", func(i *EstimatorInput) { estimatorPrior(i); i.Imports[0].Notes[0].ReviewBy = "2026-10-01" }, nil, 0, 0},
		{"FutureNotesOnly", func(i *EstimatorInput) { estimatorPrior(i); i.Imports[0].Notes[0].CreatedAt = "2026-10-03T00:00:00Z" }, nil, 0, 0},
		{"UnknownObservationEffort", func(i *EstimatorInput) { i.Imports[0].Observations[0].Subject.Effort = "" }, nil, 0, 0},
		{"UnknownCandidateEffort", func(i *EstimatorInput) {
			i.Candidates[0].Effort = canonical.Unknown
			i.Fingerprints[0].Fingerprint.Effort = ""
		}, nil, 0, 0},
		{"NoMatch", func(i *EstimatorInput) { i.Imports[0].Observations[0].Subject.Runtime = "other" }, nil, 0, 0},
		{"MissingCategory", func(i *EstimatorInput) { estimatorMissing(i, 7500) }, estBP(9000), 7500, 1},
		{"MissingCategoryPartial", func(i *EstimatorInput) { estimatorMissing(i, 7500); estimatorPartial(i) }, estBP(7000), 3750, 1},
		{"MeasuredPlusStrength", func(i *EstimatorInput) { i.Imports[0].Notes = []Note{estimatorNote()} }, estBP(8500), 10000, 2},
		{"HalfEvenFitnessDown", func(i *EstimatorInput) { i.Imports[0].Observations[0].Value = 0.2001 }, estBP(6000), 10000, 1},
		{"HalfEvenFitnessUp", func(i *EstimatorInput) { i.Imports[0].Observations[0].Value = 0.2003 }, estBP(6002), 10000, 1},
		{"HalfEvenCoverageDown", func(i *EstimatorInput) { estimatorMissing(i, 25); estimatorPartial(i) }, estBP(7000), 12, 1},
		{"CoverageRoundOnceMixedKinds", func(i *EstimatorInput) {
			// Exact terms are 0.5 and 7499.25: round(7499.75) = 7500.
			// Rounding the terms separately would give 0 + 7499 = 7499.
			estimatorMissing(i, 1)
			doc := &i.Imports[0]
			doc.Benchmarks[0].Categories = append(doc.Benchmarks[0].Categories, CategoryCoverage{"review.spec", "synthetic"})
			i.Derivation.Normalisations[0].Categories = append(i.Derivation.Normalisations[0].Categories, "review.spec")
			other := estimatorCopy(doc.Observations[0])
			doc.Observations[0].Subject.HarnessVersion = ""
			other.Categories = []string{"review.spec"}
			other.EvidenceKind = "interpolated"
			other.Provenance.OriginRef = "synthetic-origin-spec"
			doc.Observations = append(doc.Observations, other)
		}, estBP(9000), 7500, 2},
		{"CoverageRoundOnceTwoHalfTies", func(i *EstimatorInput) {
			// Exact terms are 12.5 and 12.5: round(25) = 25.
			// Rounding the terms separately would give 12 + 12 = 24.
			estimatorPartial(i)
			i.Requirements.Requirements = []Requirement{{Category: "review.code", Weight: 25}, {Category: "review.docs", Weight: 25}, {Category: "review.spec", Weight: 9950}}
			doc := &i.Imports[0]
			doc.Benchmarks[0].Categories = append(doc.Benchmarks[0].Categories, CategoryCoverage{"review.docs", "synthetic"})
			i.Derivation.Normalisations[0].Categories = append(i.Derivation.Normalisations[0].Categories, "review.docs")
			other := estimatorCopy(doc.Observations[0])
			other.Categories = []string{"review.docs"}
			other.Provenance.OriginRef = "synthetic-origin-docs"
			doc.Observations = append(doc.Observations, other)
		}, estBP(7000), 25, 2},
		{"KnownNegativeOne", func(i *EstimatorInput) {
			estimatorPrior(i)
			i.Imports[0].Notes[0].Basis = "review-outcomes"
			i.Imports[0].Notes[0].Claim.Polarity = "weakness"
		}, estBP(0), 0, 1},
		{"EmptyEvidence", func(i *EstimatorInput) { i.Imports = []Import{} }, nil, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := estimatorFixture(t, tc.mutate)
			e, estimates, p := estimatorOutput(t, input)
			got := estimates[0]
			if got.Coverage == nil {
				t.Fatal("coverage is absent")
			}
			if *got.Coverage != tc.coverage {
				t.Fatalf("coverage=%d want %d", *got.Coverage, tc.coverage)
			}
			if !reflect.DeepEqual(got.Fitness, tc.fitness) || len(got.ContributingRecordIDs) != tc.refs {
				t.Fatalf("estimate=%+v fitness=%v coverage=%d refs=%d", got, tc.fitness, tc.coverage, tc.refs)
			}
			vector := struct {
				SchemaVersion string                     `json:"schema_version"`
				Input         EstimatorInput             `json:"input"`
				View          View                       `json:"view"`
				Estimates     []routing.Estimate         `json:"estimates"`
				Partition     routing.EstimatorPartition `json:"partition"`
				InputDigest   string                     `json:"input_digest"`
				ConfigDigest  string                     `json:"config_digest"`
			}{"estimator-vector-v1", e.Input(), e.View(), estimates, p, e.InputDigest(), e.ConfigDigest()}
			estimatorGolden(t, tc.name, vector)
		})
	}
}

func estimatorGolden(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := canonical.Digest(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "estimator", name)
	if updateEstimator {
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path+".canonical.json", raw, 0644); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path+".digest", []byte(digest+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path + ".canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	id, err := os.ReadFile(path + ".digest")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) || digest != strings.TrimSpace(string(id)) {
		t.Fatalf("canonical drift in %s", name)
	}
}

func TestEstimatorTypedRefusals(t *testing.T) {
	base := estimatorFixture(t, nil)
	cases := []struct {
		name   string
		mutate func(*EstimatorInput)
		code   string
	}{
		{"schema", func(i *EstimatorInput) { i.SchemaVersion = "other" }, EstimatorInputMismatch},
		{"missing-bindings", func(i *EstimatorInput) { i.Fingerprints = []CandidateFingerprint{} }, EstimatorInputMismatch},
		{"binding-id", func(i *EstimatorInput) { i.Fingerprints[0].CandidateID = "other" }, EstimatorInputMismatch},
		{"conflicting-model", func(i *EstimatorInput) { i.Fingerprints[0].Fingerprint.ModelID = "other" }, EstimatorInputMismatch},
		{"conflicting-runtime", func(i *EstimatorInput) { i.Fingerprints[0].Fingerprint.Runtime = "other" }, EstimatorInputMismatch},
		{"conflicting-effort", func(i *EstimatorInput) { i.Fingerprints[0].Fingerprint.Effort = "max" }, EstimatorInputMismatch},
		{"conflicting-tools", func(i *EstimatorInput) { i.Fingerprints[0].Fingerprint.ToolProfile = "other" }, EstimatorInputMismatch},
		{"conflicting-engine", func(i *EstimatorInput) { i.Fingerprints[0].Fingerprint.EngineProfile.Quantization = "other" }, EstimatorInputMismatch},
		{"invented-effort", func(i *EstimatorInput) { i.Candidates[0].Effort = canonical.Unknown }, EstimatorInputMismatch},
		{"omitted-known-effort", func(i *EstimatorInput) { i.Fingerprints[0].Fingerprint.Effort = "" }, EstimatorInputMismatch},
		{"zero-weight", func(i *EstimatorInput) { i.Requirements.Requirements[0].Weight = 0 }, EstimatorRequirementsInvalid},
		{"negative-weight", func(i *EstimatorInput) { i.Requirements.Requirements[0].Weight = -1 }, EstimatorRequirementsInvalid},
		{"fractional-weight", func(i *EstimatorInput) { i.Requirements.Requirements[0].Weight = 9999.5 }, EstimatorRequirementsInvalid},
		{"wrong-weight-total", func(i *EstimatorInput) { i.Requirements.Requirements[0].Weight = 9999 }, EstimatorRequirementsInvalid},
		{"lossy-language", func(i *EstimatorInput) {
			i.Requirements.Requirements[0].Facets = &Facets{Languages: []string{"go", "swift"}}
		}, EstimatorRequirementsInvalid},
		{"lossy-platform", func(i *EstimatorInput) {
			i.Requirements.Requirements[0].Facets = &Facets{Platforms: []string{"ios", "macos"}}
		}, EstimatorRequirementsInvalid},
		{"lossy-role", func(i *EstimatorInput) {
			i.Requirements.Requirements[0].Facets = &Facets{Roles: []string{"developer", "reviewer"}}
		}, EstimatorRequirementsInvalid},
		{"wrong-config-version", func(i *EstimatorInput) { i.Config.Version = "other" }, EstimatorConfigInvalid},
		{"zero-width", func(i *EstimatorInput) { i.Config.FitnessBandWidthBP = 0 }, EstimatorConfigInvalid},
		{"negative-width", func(i *EstimatorInput) { i.Config.CoverageBandWidthBP = -1 }, EstimatorConfigInvalid},
		{"large-width", func(i *EstimatorInput) { i.Config.FitnessBandWidthBP = 10001 }, EstimatorConfigInvalid},
		{"registry-mismatch", func(i *EstimatorInput) { i.Snapshot.RegistryRef.Version = "other" }, "evidence_registry_mismatch"},
		{"snapshot-import-mismatch", func(i *EstimatorInput) { i.Snapshot.Imports = []string{} }, "evidence_snapshot_mismatch"},
		{"missing-transfer-rule", func(i *EstimatorInput) { i.Imports[0].Observations[0].EvidenceKind = "transferred" }, "evidence_transfer_required"},
		{"missing-transfer-uncertainty", func(i *EstimatorInput) { estimatorTransfer(i); i.Imports[0].Observations[0].Uncertainty = nil }, "evidence_transfer_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := estimatorCopy(base)
			tc.mutate(&input)
			_, err := NewFitnessEstimator(input)
			evalError(t, err, tc.code)
		})
	}
	e, estimates, _ := estimatorOutput(t, base)
	refs := estimatorRefs(base)
	t.Run("wrong-digest-first", func(t *testing.T) {
		_, err := e.Estimate(routing.Requirements{}, nil, routing.EvidenceSnapshotRef{Digest: "wrong"})
		var typed *routing.Error
		if !errors.As(err, &typed) || typed.Code != routing.EstimatorSnapshotMismatch {
			t.Fatalf("%T %v", err, err)
		}
	})
	for _, tc := range []struct {
		name       string
		req        routing.Requirements
		candidates []routing.ExecutionCandidate
		ref        routing.EvidenceSnapshotRef
		code       string
	}{
		{"missing-contribution", e.Requirements(), base.Candidates, routing.EvidenceSnapshotRef{Digest: refs.Digest, RecordIDs: []string{}}, EstimatorRecordRefMismatch},
		{"nil-refs", e.Requirements(), base.Candidates, routing.EvidenceSnapshotRef{Digest: refs.Digest}, EstimatorRecordRefMismatch},
		{"invalid-ref", e.Requirements(), base.Candidates, routing.EvidenceSnapshotRef{Digest: refs.Digest, RecordIDs: []string{"obs:" + strings.Repeat("f", 64)}}, EstimatorRecordRefMismatch},
		{"requirements", routing.Requirements{Items: []routing.Requirement{}}, base.Candidates, refs, EstimatorInputMismatch},
		{"full-candidates", e.Requirements(), []routing.ExecutionCandidate{}, refs, EstimatorInputMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) { _, err := e.Estimate(tc.req, tc.candidates, tc.ref); evalError(t, err, tc.code) })
	}
	for _, field := range []string{"fitness", "coverage", "version", "refs"} {
		t.Run("partition-"+field, func(t *testing.T) {
			changed := estimatorCopy(estimates)
			switch field {
			case "fitness":
				changed[0].Fitness = estBP(1)
			case "coverage":
				changed[0].Coverage = nil
			case "version":
				changed[0].EstimatorVersion = "other"
			case "refs":
				changed[0].ContributingRecordIDs = []string{}
			}
			_, err := e.Partition(changed)
			evalError(t, err, EstimatorPartitionInputMismatch)
		})
	}
	for _, field := range []string{"score", "contribution", "scale"} {
		t.Run("stored-view-"+field, func(t *testing.T) {
			view := e.View()
			switch field {
			case "score":
				*view.Results[0].Score = 0.4
			case "contribution":
				view.Results[0].Contributions[0].Weight = 0.4
				*view.Results[0].Score = 0.4
			case "scale":
				view.Results[0].Scale = Scale{0, 1}
			}
			if err := view.Validate(); field == "contribution" && err != nil {
				t.Fatal(err)
			}
			_, err := NewFitnessEstimator(base, view)
			evalError(t, err, EstimatorViewMismatch)
		})
	}
}

// This independent oracle uses exact input utility and decimal priors, with
// integer cross-products for expected round-to-even, not W2 projection code.
func estimatorOracleBP(t *testing.T, utility string) int64 {
	t.Helper()
	u, ok := new(big.Rat).SetString(utility)
	if !ok {
		t.Fatal(utility)
	}
	n := new(big.Int).Add(u.Num(), u.Denom())
	n.Mul(n, big.NewInt(5000))
	q, r := new(big.Int), new(big.Int)
	q.DivMod(n, u.Denom(), r)
	twice := new(big.Int).Mul(r, big.NewInt(2))
	if twice.Cmp(u.Denom()) > 0 || twice.Cmp(u.Denom()) == 0 && q.Bit(0) != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Int64()
}

func TestEstimatorAllNotePriorsAndExpiry(t *testing.T) {
	for _, confidence := range []struct{ key, value string }{{"low", "0.25"}, {"medium", "0.6"}, {"high", "1"}} {
		for _, basis := range []struct{ key, value string }{{"benchmark-reading", "0.4"}, {"incident", "0.8"}, {"operator-judgement", "0.6"}, {"review-outcomes", "1"}, {"run-outcomes", "0.8"}} {
			for _, partial := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/partial=%t", confidence.key, basis.key, partial), func(t *testing.T) {
					input := estimatorFixture(t, func(i *EstimatorInput) {
						estimatorPrior(i)
						i.Imports[0].Notes[0].Confidence = confidence.key
						i.Imports[0].Notes[0].Basis = basis.key
						if partial {
							estimatorPartial(i)
						}
					})
					_, estimates, _ := estimatorOutput(t, input)
					c, _ := new(big.Rat).SetString(confidence.value)
					b, _ := new(big.Rat).SetString(basis.value)
					u := new(big.Rat).Mul(c, b)
					if partial {
						u.Quo(u, big.NewRat(2, 1))
					}
					if *estimates[0].Fitness != estimatorOracleBP(t, u.RatString()) || *estimates[0].Coverage != 0 {
						t.Fatalf("%+v", estimates[0])
					}
				})
			}
		}
	}
	for _, tc := range []struct {
		name, review, at string
		usable           bool
	}{
		{"date-whole-day", "2026-10-02", "2026-10-02T23:59:59.999999999Z", true},
		{"date-next-day", "2026-10-02", "2026-10-03T00:00:00Z", false},
		{"timestamp-exact", "2026-10-02T12:00:00Z", "2026-10-02T12:00:00Z", true},
		{"timestamp-after", "2026-10-02T12:00:00Z", "2026-10-02T12:00:00.000000001Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := estimatorFixture(t, func(i *EstimatorInput) {
				estimatorPrior(i)
				i.Imports[0].Notes[0].ReviewBy = tc.review
				i.EvaluatedAt = tc.at
			})
			e, estimates, _ := estimatorOutput(t, input)
			if (estimates[0].Fitness != nil) != tc.usable {
				t.Fatal(estimates)
			}
			c := e.View().Results[0].Contributions[0]
			if c.AgeS == nil || c.Stale == tc.usable || !tc.usable && c.Weight != 0 {
				t.Fatalf("%+v", c)
			}
		})
	}
}
