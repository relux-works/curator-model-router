package evidence

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

const EstimatorReplaySchemaVersion = "estimator-replay-v1"

type EstimatorReplayBundle struct {
	SchemaVersion string                 `json:"schema_version"`
	Input         EstimatorInput         `json:"input"`
	View          View                   `json:"view"`
	Routing       routing.DecisionBundle `json:"routing"`
}

func (e *ViewEstimator) EvaluatorVersions() []routing.Provenance {
	return []routing.Provenance{
		{Name: "fitness-estimator", Version: EstimatorVersion},
		{Name: "fitness-input", Version: EstimatorInputSchemaVersion, Digest: e.inputDigest},
		{Name: "role-suitability", Version: e.input.Derivation.Version, Digest: e.view.Inputs.DerivationDigest},
		{Name: "role-suitability-view", Version: SchemaVersion, Digest: e.view.ID},
	}
}

// BuildEvaluation binds the complete regenerated view, input and estimates. The
// caller chooses whether to include a partition; absence stays absent in replay.
func (e *ViewEstimator) BuildEvaluation(rubric routing.Rubric, ref routing.EvidenceSnapshotRef, includePartition bool) (routing.EvaluationSnapshot, error) {
	v := routing.EvaluationSnapshot{SchemaVersion: "v1", EvidenceSnapshotDigest: e.snapshotDigest, Measurements: []string{}, Assessments: []routing.Assessment{}, Estimates: []routing.Estimate{}, EvaluatorVersions: e.EvaluatorVersions()}
	if rubric.ID == "" || rubric.Version == "" || !estimatorEqual(rubric.Requirements, e.requirements) {
		return v, refuse(EstimatorInputMismatch, "rubric does not bind projected requirements")
	}
	estimates, err := e.Estimate(rubric.Requirements, e.input.Candidates, ref)
	if err != nil {
		return v, err
	}
	frozen := estimatorCopy(rubric)
	v.Rubric = &frozen
	v.Measurements = estimatorCopy(ref.RecordIDs)
	v.Estimates = estimates
	if includePartition {
		p, err := e.Partition(estimates)
		if err != nil {
			return v, err
		}
		v.EstimatorPartition = &p
	}
	return v, v.ValidateAgainst(e.input.Candidates)
}

// ReplayEvaluation verifies frozen input bindings, compares regenerated content
// before selection, and never replaces the stored evaluation or partition.
// With no stored decision it only audits estimation. A supplied decision also
// invokes routing's version-aware selection replay once estimation agrees.
func ReplayEvaluation(b EstimatorReplayBundle, stored ...*routing.RoutingDecision) (routing.ReplayResult, error) {
	result := routing.ReplayResult{Differences: []routing.MemberDifference{}}
	if b.SchemaVersion != EstimatorReplaySchemaVersion || len(stored) > 1 {
		return result, refuse(EstimatorInputMismatch, "invalid replay capsule")
	}
	e, err := NewFitnessEstimator(b.Input)
	if err != nil {
		return result, err
	}
	evaluation := b.Routing.Evaluation
	if evaluation.EvidenceSnapshotDigest != e.snapshotDigest {
		return result, routing.NewEstimatorSnapshotMismatchError()
	}
	if !estimatorEqual(b.Routing.Snapshot.Candidates, e.input.Candidates) || b.Routing.Envelope.Role != e.input.Requirements.Role || evaluation.Rubric == nil || !estimatorEqual(evaluation.Rubric.Requirements, e.requirements) || b.Routing.Versions.EstimatorVersion != EstimatorVersion {
		return result, refuse(EstimatorInputMismatch, "routing candidates, role, rubric or estimator version differ from capsule")
	}
	for _, a := range evaluation.Assessments {
		if !estimatorEqual(a.Requirements, e.requirements) {
			return result, refuse(EstimatorInputMismatch, "assessment requirements differ from capsule")
		}
	}
	if err = canonical.CheckOrdered(evaluation.EvaluatorVersions, func(p routing.Provenance) string { return p.Name }); err != nil {
		return result, err
	}
	for _, expected := range e.EvaluatorVersions() {
		found := false
		for _, actual := range evaluation.EvaluatorVersions {
			if actual.Name == expected.Name {
				found = estimatorEqual(actual, expected)
				break
			}
		}
		if !found {
			return result, refuse(EstimatorInputMismatch, "evaluation provenance differs from frozen estimator: "+expected.Name)
		}
	}
	estimates, err := e.Estimate(e.requirements, e.input.Candidates, routing.EvidenceSnapshotRef{Digest: e.snapshotDigest, RecordIDs: evaluation.Measurements})
	if err != nil {
		return result, err
	}
	if err = estimatorCompare("$.view", b.View, e.view, &result.Differences); err != nil {
		return result, err
	}
	if err = estimatorCompare("$.routing.evaluation.estimates", evaluation.Estimates, estimates, &result.Differences); err != nil {
		return result, err
	}
	if evaluation.EstimatorPartition != nil {
		partition, err := e.Partition(estimates)
		if err != nil {
			return result, err
		}
		if err = evaluation.EstimatorPartition.ValidateAgainst(e.input.Candidates, evaluation.Estimates); err != nil {
			return result, err
		}
		if !estimatorEqual(evaluation.EstimatorPartition, partition) {
			// Membership is one opaque partition comparison; do not let selection
			// consume substituted membership, even when structurally self-consistent.
			left, _ := json.Marshal(evaluation.EstimatorPartition)
			right, _ := json.Marshal(partition)
			result.Differences = append(result.Differences, routing.MemberDifference{Member: "$.routing.evaluation.estimator_partition", Stored: left, Recomputed: right})
		}
	}
	result.Equal = len(result.Differences) == 0
	if result.Equal && len(stored) == 1 {
		selection, err := routing.Replay(b.Routing, stored[0])
		if err != nil {
			return result, err
		}
		for _, d := range selection.Differences {
			d.Member = "$.routing.decision" + d.Member[1:]
			result.Differences = append(result.Differences, d)
		}
		result.Equal = selection.Equal
	}
	return result, nil
}

func estimatorCompare(path string, a, b any, diffs *[]routing.MemberDifference) error {
	raw := func(v any) (json.RawMessage, error) {
		wrapper, err := canonical.MarshalRecord(struct {
			Value any `json:"value"`
		}{v})
		if err != nil {
			return nil, err
		}
		var object map[string]json.RawMessage
		err = json.Unmarshal(wrapper, &object)
		return object["value"], err
	}
	left, err := raw(a)
	if err != nil {
		return err
	}
	right, err := raw(b)
	if err != nil {
		return err
	}
	estimatorCompareMembers(path, left, right, diffs)
	return nil
}

func estimatorCompareMembers(path string, a, b json.RawMessage, diffs *[]routing.MemberDifference) {
	if string(a) == string(b) {
		return
	}
	if len(a) > 0 && len(b) > 0 && a[0] == '{' && b[0] == '{' {
		var left, right map[string]json.RawMessage
		_ = json.Unmarshal(a, &left)
		_ = json.Unmarshal(b, &right)
		keys := map[string]bool{}
		for k := range left {
			keys[k] = true
		}
		for k := range right {
			keys[k] = true
		}
		ordered := []string{}
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		for _, k := range ordered {
			estimatorCompareMembers(path+"."+k, left[k], right[k], diffs)
		}
		return
	}
	if len(a) > 0 && len(b) > 0 && a[0] == '[' && b[0] == '[' {
		var left, right []json.RawMessage
		_ = json.Unmarshal(a, &left)
		_ = json.Unmarshal(b, &right)
		for i := 0; i < max(len(left), len(right)); i++ {
			var l, r json.RawMessage
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			estimatorCompareMembers(fmt.Sprintf("%s[%d]", path, i), l, r, diffs)
		}
		return
	}
	*diffs = append(*diffs, routing.MemberDifference{Member: path, Stored: a, Recomputed: b})
}
