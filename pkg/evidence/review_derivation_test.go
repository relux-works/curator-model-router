package evidence

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func TestReviewProtocolCountsVectors(t *testing.T) {
	var f struct {
		SchemaVersion string `json:"schema_version"`
		Vectors       []struct {
			Name     string          `json:"name"`
			Counts   ReviewCounts    `json:"counts"`
			Expected ReviewAggregate `json:"expected"`
		} `json:"vectors"`
	}
	if err := Decode(evalFixture(t, "counts.json"), &f); err != nil {
		t.Fatal(err)
	}
	for _, v := range f.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			a, err := AggregateReview(v.Counts)
			if err != nil || !reflect.DeepEqual(a, v.Expected) {
				t.Fatalf("aggregate=%+v error=%v expected=%+v", a, err, v.Expected)
			}
		})
	}
	for _, v := range []ReviewCounts{{ExpectedCases: 1, TrueDefectsFound: -1}, {ExpectedCases: 1, EvaluatedCases: 2}, {ExpectedCases: 1, CorrectCleanReviews: 1}, {}} {
		_, err := AggregateReview(v)
		evalError(t, err, "evidence_evalrun_invalid_result")
	}
	t.Run("NoCountOverflow", func(t *testing.T) {
		a, err := AggregateReview(ReviewCounts{ExpectedCases: 1, EvaluatedCases: 1, TrueDefectsFound: math.MaxInt64, FalseFindings: math.MaxInt64, MissedDefects: math.MaxInt64})
		if err != nil || a.ReviewF1 == nil || *a.ReviewF1 != 0.5 {
			t.Fatalf("aggregate=%+v error=%v", a, err)
		}
	})
}
func TestReviewDerivationV2ArtifactAndV1Unchanged(t *testing.T) {
	d := ReviewDerivationV2()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := canonical.Marshal(d)
	if err != nil || !bytes.Equal(b, evalFixture(t, "review-derivation-v2.json")) {
		t.Fatalf("v2 artifact drift: %s %v", b, err)
	}
	id, _ := canonical.Digest(d)
	if id != strings.TrimSpace(string(evalFixture(t, "review-derivation-v2.digest"))) {
		t.Fatal("v2 digest drift")
	}
	old := DefaultDerivation()
	id, _ = canonical.Digest(old)
	if id != strings.TrimSpace(string(evalFixture(t, "review-derivation-v1.digest"))) || old.Version != "1" {
		t.Fatal("v1 bytes drift")
	}
	if len(d.Normalisations) != len(old.Normalisations)+1 || !reflect.DeepEqual(d.Normalisations[:len(old.Normalisations)], old.Normalisations) {
		t.Fatal("Bug Hunt mapping changed")
	}
	// Both versions must own their mutable arrays.
	d.ConfidenceWeights[0].Value = 0
	d.Normalisations[0].Categories[0] = "changed"
	if DefaultDerivation().ConfidenceWeights[0].Value != 1 || ReviewDerivationV2().Normalisations[0].Categories[0] != "code.fix" {
		t.Fatal("shared mutable defaults")
	}
}
func TestReviewDerivationNumericVectors(t *testing.T) {
	var f struct {
		SchemaVersion string `json:"schema_version"`
		Vectors       []struct {
			Name         string            `json:"name"`
			Candidate    Fingerprint       `json:"candidate"`
			Requirements RequirementsInput `json:"requirements"`
			Version      string            `json:"derivation_version"`
			Expected     struct {
				Utility  *float64 `json:"utility,omitempty"`
				Fitness  *int64   `json:"fitness_bp,omitempty"`
				Coverage int64    `json:"coverage_bp"`
			} `json:"expected"`
		} `json:"vectors"`
	}
	if err := Decode(evalFixture(t, "derivation-vectors.json"), &f); err != nil {
		t.Fatal(err)
	}
	v, m, r := evalInputs(t)
	doc := evalDoc(t, v, m, r)
	docs := []Import{doc}
	snap := snapshotFor(t, r, docs)
	for _, v := range f.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			d := ReviewDerivationV2()
			if v.Version == "1" {
				d = DefaultDerivation()
			}
			view, err := Derive(snap, docs, r, v.Requirements, d, []Fingerprint{v.Candidate}, "2026-10-02T00:00:00Z")
			if err != nil {
				t.Fatal(err)
			}
			if err = view.Validate(); err != nil {
				t.Fatal(err)
			}
			b, err := canonical.Marshal(view)
			if err != nil || !bytes.Equal(b, evalFixture(t, v.Name+".view.json")) {
				t.Fatalf("view golden drift: %s error=%v", b, err)
			}
			id, _ := canonical.Digest(view)
			if id != strings.TrimSpace(string(evalFixture(t, v.Name+".view.digest"))) {
				t.Fatal("view digest drift")
			}
			s := view.Results[0]
			if !reflect.DeepEqual(s.Score, v.Expected.Utility) {
				t.Fatalf("utility=%v expected=%v", s.Score, v.Expected.Utility)
			}
			fitness, coverage := exactProjection(s, v.Requirements, d)
			if !reflect.DeepEqual(fitness, v.Expected.Fitness) || coverage != v.Expected.Coverage {
				t.Fatalf("fitness=%v coverage=%d expected=%+v", fitness, coverage, v.Expected)
			}
			wantIssues := 3
			if v.Version == "1" {
				wantIssues = 4
			}
			if len(view.Issues) != wantIssues {
				t.Fatalf("diagnostics=%+v", view.Issues)
			}
			for _, i := range view.Issues {
				if i.Code != "observation_unmapped" {
					t.Fatalf("unexpected issue %+v", i)
				}
			}
		})
	}
}
func TestReviewNewBenchmarkVersionUnmapped(t *testing.T) {
	v, m, r := evalInputs(t)
	v.Benchmark.Version = "2"
	doc := evalDoc(t, v, m, r)
	view := evalView(t, []Import{doc}, r, ReviewDerivationV2(), doc.Observations[0].Subject)
	if view.Results[0].Score != nil || len(view.Issues) != 4 {
		t.Fatal("new version mapped without declaration")
	}
}
func TestReviewProtocolManifestArtifacts(t *testing.T) {
	for _, name := range []string{"protocol-template", "manifest-template"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join("..", "..", "spec", "review-set")
			raw, err := os.ReadFile(filepath.Join(root, name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			b, err := canonical.Canonicalize(raw)
			if err != nil || !bytes.Equal(raw, b) {
				t.Fatalf("artifact not canonical: %v", err)
			}
			var obj map[string]json.RawMessage
			if err = json.Unmarshal(raw, &obj); err != nil {
				t.Fatal(err)
			}
			id, err := canonical.Digest(obj)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(root, name+".digest"))
			if err != nil || id != strings.TrimSpace(string(want)) {
				t.Fatal("content address drift")
			}
			if string(obj["status"]) != `"uncollected-template"` {
				t.Fatal("template misrepresents collection")
			}
			if name == "protocol-template" {
				manifest, err := os.ReadFile(filepath.Join(root, "manifest-template.digest"))
				if err != nil {
					t.Fatal(err)
				}
				var bound string
				if err = json.Unmarshal(obj["corpus_manifest_digest"], &bound); err != nil || bound != strings.TrimSpace(string(manifest)) {
					t.Fatal("manifest binding drift")
				}
				for _, key := range []string{"ground_truth", "case_weighting", "severity_reporting", "task_class", "completion_rule", "cost_denominator", "sample_count_definition"} {
					if obj[key] == nil {
						t.Fatalf("missing %s", key)
					}
				}
			} else if string(obj["cases"]) != "[]" {
				t.Fatal("invented corpus")
			}
		})
	}
}
