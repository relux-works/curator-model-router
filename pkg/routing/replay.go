package routing

import (
	"encoding/json"
	"fmt"
	"slices"
)

type MemberDifference struct {
	Member     string          `json:"member"`
	Stored     json.RawMessage `json:"stored,omitempty"`
	Recomputed json.RawMessage `json:"recomputed,omitempty"`
}
type ReplayResult struct {
	Equal       bool               `json:"equal"`
	Differences []MemberDifference `json:"differences"`
}

// Replay compares JSON members recursively, preserving exact integer tokens.
// Missing members are omitted in the difference rather than rendered as null.
// Quality replay follows the stored stage: router decisions precede fallback;
// baseline_after_abstain decisions include the bundle-authorized fallback.
func Replay(b DecisionBundle, stored *RoutingDecision) (ReplayResult, error) {
	if b.Policy.Mode != ModeOff {
		if err := validateQualitySelectorInput(&b.Snapshot, &b.Evaluation, b.Policy, b.Versions.SelectorVersion, &b.Versions.EstimatorVersion); err != nil {
			return ReplayResult{}, err
		}
	}
	replayStage := b.Versions.SelectorVersion == QualitySelectorVersion && stored != nil &&
		(stored.SelectionOrigin == OriginRouter || stored.SelectionOrigin == OriginBaselineAfterAbstain)
	fallbackOptions := b.Versions
	if replayStage {
		// Rebuild the strategy decision before replaying any recorded fallback.
		b.Versions.BaselineFallbackReasons = nil
	}
	recomputed, err := Route(b)
	if err != nil {
		return ReplayResult{}, err
	}
	if replayStage && stored.SelectionOrigin == OriginBaselineAfterAbstain && recomputed.Decision != nil && stored.SelectedCandidate != nil {
		// The selected variant records BaselineFallback's baseline input. The
		// public transformation permits any identical admitted snapshot variant.
		fallback, err := BaselineFallback(*recomputed.Decision, b.Snapshot, *stored.SelectedCandidate,
			fallbackOptions.BaselineFallbackReasons, fallbackOptions.MandatoryEvidenceFloor || b.Policy.Quality != nil)
		if err != nil {
			return ReplayResult{}, err
		}
		recomputed.Decision = &fallback
	}
	result := ReplayResult{Differences: []MemberDifference{}}
	raw := func(v *RoutingDecision) json.RawMessage {
		if v == nil {
			return nil
		}
		r, _ := json.Marshal(v)
		return r
	}
	compareMembers("$", raw(stored), raw(recomputed.Decision), &result.Differences)
	result.Equal = len(result.Differences) == 0
	return result, nil
}
func compareMembers(path string, a, b json.RawMessage, diffs *[]MemberDifference) {
	if string(a) == string(b) {
		return
	}
	if len(a) > 0 && len(b) > 0 && a[0] == '{' && b[0] == '{' {
		var left, right map[string]json.RawMessage
		_ = json.Unmarshal(a, &left)
		_ = json.Unmarshal(b, &right)
		keys := []string{}
		for k := range left {
			keys = append(keys, k)
		}
		for k := range right {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		keys = slices.Compact(keys)
		for _, k := range keys {
			compareMembers(path+"."+k, left[k], right[k], diffs)
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
			compareMembers(fmt.Sprintf("%s[%d]", path, i), l, r, diffs)
		}
		return
	}
	*diffs = append(*diffs, MemberDifference{path, a, b})
}

// ExplainDecision refuses substituted inputs before rendering a stored decision.
func ExplainDecision(b DecisionBundle, d RoutingDecision) (Explanation, error) {
	if err := validateQualitySelectorInput(&b.Snapshot, &b.Evaluation, b.Policy, b.Versions.SelectorVersion, &b.Versions.EstimatorVersion); err != nil {
		return Explanation{}, err
	}
	if err := d.VerifyContentID(); err != nil {
		return Explanation{}, err
	}
	r, err := Replay(b, &d)
	if err != nil {
		return Explanation{}, err
	}
	if !r.Equal {
		return Explanation{}, fail("routing_decision_input_mismatch", "stored decision differs from supplied inputs")
	}
	ex, err := Explain(b)
	if err != nil {
		return ex, err
	}
	ex.Outcome = d.Outcome
	ex.SelectionOrigin = d.SelectionOrigin
	if d.SelectedCandidate != nil {
		ex.SelectedCandidateID = d.SelectedCandidate.ID
	}
	if d.SelectionOrigin == OriginBaselineAfterAbstain {
		for i := range ex.Candidates {
			ex.Candidates[i].FinalPosition = ptr64(ex.Candidates[i].BasePosition)
		}
	}
	return ex, nil
}
