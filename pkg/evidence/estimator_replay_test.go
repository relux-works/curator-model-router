package evidence

import (
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

func estimatorReplayFixture(t *testing.T, partition bool) (EstimatorReplayBundle, *routing.RoutingDecision) {
	t.Helper()
	input := estimatorFixture(t, func(i *EstimatorInput) {
		c := estimatorCopy(i.Candidates[0])
		c.ID = "b"
		c.RuntimeBindingID = "home-b"
		c.BillingClass = routing.BillingLocal
		i.Candidates = append(i.Candidates, c)
		i.Fingerprints = append(i.Fingerprints, CandidateFingerprint{"b", estimatorCopy(i.Fingerprints[0].Fingerprint)})
	})
	e, _, _ := estimatorOutput(t, input)
	evaluation, err := e.BuildEvaluation(routing.Rubric{ID: "synthetic-rubric", Version: "1", Requirements: e.Requirements()}, estimatorRefs(input), partition)
	if err != nil {
		t.Fatal(err)
	}
	policy := routing.DefaultPolicy("v1")
	policy.Mode = routing.ModeRecommend
	bundle := routing.DecisionBundle{SchemaVersion: "v1", Envelope: routing.TaskEnvelope{SchemaVersion: "v1", Description: "Synthetic task", SubstantiveRevision: "1", Role: "reviewer", Criteria: []string{}, Context: []string{}, Tools: []string{}}, Snapshot: routing.CandidateSnapshot{SchemaVersion: "v1", AsOf: 1790899200, ScopeMap: routing.ScopeMap{Version: "1", Entries: []routing.ScopeMapEntry{}}, UsageFacts: []routing.UsageFact{}, Inflight: []routing.Inflight{}, Candidates: input.Candidates, ConfigOrder: []string{"b", "a"}, Source: routing.Provenance{Name: "synthetic"}}, Evaluation: evaluation, Policy: policy, Versions: routing.DecisionOptions{AssessorVersion: canonical.Unknown, EstimatorVersion: EstimatorVersion, SelectorVersion: routing.SelectorVersion, ApplicabilityScope: "synthetic-project"}}
	capsule := EstimatorReplayBundle{EstimatorReplaySchemaVersion, e.Input(), e.View(), bundle}
	selected, err := routing.Route(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Decision == nil {
		t.Fatal("expected a decision")
	}
	return capsule, selected.Decision
}

func TestEstimatorReplay(t *testing.T) {
	for _, partition := range []bool{false, true} {
		name := "AbsentPartition"
		if partition {
			name = "StoredPartition"
		}
		t.Run(name, func(t *testing.T) {
			capsule, decision := estimatorReplayFixture(t, partition)
			before, err := canonical.Marshal(capsule)
			if err != nil {
				t.Fatal(err)
			}
			result, err := ReplayEvaluation(capsule, decision)
			if err != nil || !result.Equal || result.Differences == nil || len(result.Differences) != 0 {
				t.Fatalf("%+v %v", result, err)
			}
			after, err := canonical.Marshal(capsule)
			if err != nil || string(before) != string(after) || (capsule.Routing.Evaluation.EstimatorPartition != nil) != partition {
				t.Fatal("replay changed frozen evaluation")
			}
			estimatorGolden(t, "Replay"+name, struct {
				SchemaVersion string                   `json:"schema_version"`
				Capsule       EstimatorReplayBundle    `json:"capsule"`
				Decision      *routing.RoutingDecision `json:"decision"`
				Replay        routing.ReplayResult     `json:"replay"`
			}{"estimator-replay-vector-v1", capsule, decision, result})
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*EstimatorReplayBundle)
		path   string
	}{
		{"self-consistent-view", func(b *EstimatorReplayBundle) {
			b.View.Results[0].Contributions[0].Weight = 0.4
			*b.View.Results[0].Score = 0.4
		}, "$.view.results[0].contributions[0].weight"},
		{"view-score", func(b *EstimatorReplayBundle) { *b.View.Results[0].Score = 0.4 }, "$.view.results[0].score"},
		{"fitness", func(b *EstimatorReplayBundle) { b.Routing.Evaluation.Estimates[0].Fitness = estBP(1) }, "$.routing.evaluation.estimates[0].fitness"},
		{"contribution-ids", func(b *EstimatorReplayBundle) { b.Routing.Evaluation.Estimates[0].ContributingRecordIDs = []string{} }, "$.routing.evaluation.estimates[0].contributing_record_ids[0]"},
		{"partition-membership", func(b *EstimatorReplayBundle) {
			p := b.Routing.Evaluation.EstimatorPartition
			p.Groups = []routing.EstimatorPartitionGroup{{ID: "g-a", CandidateIDs: []string{"a"}}, {ID: "g-b", CandidateIDs: []string{"b"}}}
		}, "$.routing.evaluation.estimator_partition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capsule, decision := estimatorReplayFixture(t, true)
			tc.mutate(&capsule)
			result, err := ReplayEvaluation(capsule, decision)
			if err != nil || result.Equal {
				t.Fatalf("%+v %v", result, err)
			}
			found := false
			for _, d := range result.Differences {
				if d.Member == tc.path {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tc.path, result)
			}
			if tc.name == "self-consistent-view" {
				if err := capsule.View.Validate(); err != nil {
					t.Fatal("fixture must be self-consistent", err)
				}
			}
			estimatorGolden(t, "ReplayTamper-"+tc.name, struct {
				SchemaVersion string                `json:"schema_version"`
				Capsule       EstimatorReplayBundle `json:"capsule"`
				Result        routing.ReplayResult  `json:"result"`
			}{"estimator-replay-difference-v1", capsule, result})
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*EstimatorReplayBundle)
		code   string
	}{
		{"evidence-digest", func(b *EstimatorReplayBundle) {
			b.Routing.Evaluation.EvidenceSnapshotDigest = "sha256:" + strings.Repeat("b", 64)
		}, routing.EstimatorSnapshotMismatch},
		{"role", func(b *EstimatorReplayBundle) { b.Routing.Envelope.Role = "developer" }, EstimatorInputMismatch},
		{"rubric", func(b *EstimatorReplayBundle) { *b.Routing.Evaluation.Rubric.Requirements.Items[0].WeightBP = 1 }, EstimatorInputMismatch},
		{"candidate", func(b *EstimatorReplayBundle) { b.Routing.Snapshot.Candidates[0].RuntimeBindingID = "other" }, EstimatorInputMismatch},
		{"time-provenance", func(b *EstimatorReplayBundle) { b.Input.EvaluatedAt = "2026-10-02T00:00:01Z" }, EstimatorInputMismatch},
		{"project-provenance", func(b *EstimatorReplayBundle) { b.Input.Requirements.Project = "different" }, EstimatorInputMismatch},
		{"input-digest-provenance", func(b *EstimatorReplayBundle) {
			b.Routing.Evaluation.EvaluatorVersions[1].Digest = "sha256:" + strings.Repeat("f", 64)
		}, EstimatorInputMismatch},
		{"missing-provenance", func(b *EstimatorReplayBundle) { b.Routing.Evaluation.EvaluatorVersions = []routing.Provenance{} }, EstimatorInputMismatch},
		{"measurement-ref", func(b *EstimatorReplayBundle) { b.Routing.Evaluation.Measurements = []string{} }, EstimatorRecordRefMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capsule, _ := estimatorReplayFixture(t, true)
			tc.mutate(&capsule)
			_, err := ReplayEvaluation(capsule)
			if tc.code == routing.EstimatorSnapshotMismatch {
				typed, ok := err.(*routing.Error)
				if !ok || typed.Code != tc.code {
					t.Fatalf("%T %v", err, err)
				}
			} else {
				evalError(t, err, tc.code)
			}
		})
	}
	t.Run("partition-required-by-current-selector", func(t *testing.T) {
		capsule, decision := estimatorReplayFixture(t, false)
		capsule.Routing.Policy.Headroom.Enabled = true
		capsule.Routing.Policy.Headroom.Equivalence = routing.FitnessBand
		_, err := ReplayEvaluation(capsule, decision)
		typed, ok := err.(*routing.Error)
		if !ok || typed.Code != routing.HeadroomPartitionRequired {
			t.Fatalf("%T %v", err, err)
		}
	})
	t.Run("decision-selection-mismatch", func(t *testing.T) {
		capsule, decision := estimatorReplayFixture(t, false)
		decision.SelectionOrigin = routing.OriginManual
		result, err := ReplayEvaluation(capsule, decision)
		if err != nil || result.Equal || len(result.Differences) == 0 || result.Differences[0].Member != "$.routing.decision.selection_origin" {
			t.Fatalf("%+v %v", result, err)
		}
	})
}

func TestEstimatorInputReplayIdentity(t *testing.T) {
	original := estimatorFixture(t, nil)
	e, estimates, p := estimatorOutput(t, original)
	raw, err := canonical.Marshal(e.Input())
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip EstimatorInput
	if err = Decode(raw, &roundtrip); err != nil {
		t.Fatal(err)
	}
	other, out, partition := estimatorOutput(t, roundtrip)
	if !estimatorEqual(e.View(), other.View()) || !reflect.DeepEqual(estimates, out) || !estimatorEqual(p, partition) || e.InputDigest() != other.InputDigest() {
		t.Fatal("reestimation drift")
	}
	if _, err = NewFitnessEstimator(roundtrip, e.View()); err != nil {
		t.Fatal(err)
	}
	// Extra valid but inapplicable addresses are allowed, not a hidden filter.
	input := estimatorFixture(t, func(i *EstimatorInput) {
		o := estimatorCopy(i.Imports[0].Observations[0])
		o.Subject.Runtime = "other"
		i.Imports[0].Observations = append(i.Imports[0].Observations, o)
	})
	_, out, _ = estimatorOutput(t, input)
	if len(out[0].ContributingRecordIDs) != 1 {
		t.Fatal(out)
	}
}
