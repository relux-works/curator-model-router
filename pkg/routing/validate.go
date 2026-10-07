package routing

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string        { return e.Code + ": " + e.Message }
func fail(code, message string) error { return &Error{code, message} }

func (v CandidateSnapshot) Validate() error {
	if err := validateFields(v); err != nil {
		return err
	}
	if !timestampValid(v.AsOf) {
		return fail("routing_invalid_as_of", "as_of outside supported timestamp range")
	}
	checks := []error{
		canonical.CheckOrdered(v.UsageFacts, func(v UsageFact) string { return v.Key }),
		canonical.CheckOrdered(v.Inflight, func(v Inflight) string { return v.Key }),
		canonical.CheckOrdered(v.Candidates, func(v ExecutionCandidate) string { return v.ID }),
		checkScopeEntries(v.ScopeMap.Entries),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	for _, entry := range v.ScopeMap.Entries {
		if err := canonical.CheckOrdered(entry.ModelIDs, func(v string) string { return v }); err != nil {
			return err
		}
	}
	for _, fact := range v.UsageFacts {
		switch fact.State {
		case StateExact, StatePercentOnly, StateLastObserved, StateNotSupported, StateUnavailable, StateAbsent:
		default:
			return fail("routing_unknown_enum", "unknown usage state")
		}
		if fact.FailureCount < 0 {
			return fail("routing_invalid_count", "negative failure count")
		}
		if err := canonical.CheckOrdered(fact.Windows, func(v UsageWindow) string { return v.ID }); err != nil {
			return err
		}
		for _, w := range fact.Windows {
			switch w.Freshness {
			case Fresh, Stale, Expired:
				// TTL changes fresh versus stale only; validity and expiry are fixed.
				actual := ClassifyFreshness(w, v.AsOf, MinTTL)
				if actual == Invalid || (w.Freshness == Expired) != (actual == Expired) {
					return fail(ContractFreshnessMismatch, "window freshness contradicts TTL-independent facts")
				}
			case Invalid:
			default:
				return fail("routing_unknown_enum", "unknown freshness")
			}
		}
	}
	for _, count := range v.Inflight {
		if count.Runs < 0 {
			return fail("routing_invalid_count", "negative inflight count")
		}
	}
	for _, c := range v.Candidates {
		if err := validateCandidate(c); err != nil {
			return err
		}
	}
	return checkConfigOrder(v.ConfigOrder, v.Candidates)
}

func checkConfigOrder(order []string, candidates []ExecutionCandidate) error {
	ids := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		ids[candidate.ID] = false
	}
	for _, id := range order {
		seen, exists := ids[id]
		if !exists {
			return fail(ContractConfigOrderMismatch, fmt.Sprintf("config_order contains extra candidate id %q", id))
		}
		if seen {
			return fail(ContractConfigOrderMismatch, fmt.Sprintf("config_order duplicates candidate id %q", id))
		}
		ids[id] = true
	}
	// Candidates are already sorted, so missing-id refusals are deterministic.
	for _, candidate := range candidates {
		if !ids[candidate.ID] {
			return fail(ContractConfigOrderMismatch, fmt.Sprintf("config_order is missing candidate id %q", candidate.ID))
		}
	}
	return nil
}
func validateCandidate(c ExecutionCandidate) error {
	if err := validateFields(c); err != nil {
		return err
	}
	switch c.BillingClass {
	case BillingLocal, BillingMetered:
	case BillingSubscription:
		if c.UsageKey == nil || *c.UsageKey == "" {
			return fail("routing_missing_usage_key", "subscription candidate requires usage_key")
		}
	default:
		return fail("routing_unknown_enum", "unknown billing_class")
	}
	return nil
}
func checkScopeEntries(entries []ScopeMapEntry) error {
	compare := func(a, b ScopeMapEntry) int {
		if n := cmp.Compare(a.Runtime, b.Runtime); n != 0 {
			return n
		}
		return cmp.Compare(a.Scope, b.Scope)
	}
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, compare)
	for i := 1; i < len(sorted); i++ {
		if compare(sorted[i-1], sorted[i]) == 0 {
			return &canonical.Error{Code: canonical.DuplicateKey, Message: "duplicate runtime/scope"}
		}
	}
	for i := 1; i < len(entries); i++ {
		if compare(entries[i-1], entries[i]) > 0 {
			return &canonical.Error{Code: canonical.Unordered, Message: "scope entries must be ordered by runtime then scope"}
		}
	}
	return nil
}
func checkAddresses(refs []string, allowRetraction bool) error {
	if err := canonical.CheckOrdered(refs, func(v string) string { return v }); err != nil {
		return err
	}
	for _, ref := range refs {
		if !validAddress(ref, allowRetraction) {
			return fail("routing_invalid_evidence_ref", "invalid evidence record address")
		}
	}
	return nil
}
func (v EvaluationSnapshot) Validate() error {
	if err := v.validateContent(); err != nil {
		return err
	}
	return v.validatePartition()
}

// validateContent keeps structural and estimate checks independent of partition
// membership so quality selection can first bind estimates to admission.
func (v EvaluationSnapshot) validateContent() error {
	if err := validateFields(v); err != nil {
		return err
	}
	checks := []error{
		canonical.CheckOrdered(v.Assessments, func(v Assessment) string { return v.ID }),
		canonical.CheckOrdered(v.Estimates, func(v Estimate) string { return v.CandidateID }),
		canonical.CheckOrdered(v.EvaluatorVersions, func(v Provenance) string { return v.Name }),
		checkAddresses(v.Measurements, false),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	for _, e := range v.Estimates {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	if v.Costs != nil {
		if err := v.Costs.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (v EvaluationSnapshot) validatePartition() error {
	if v.EstimatorPartition != nil {
		// An evaluation can check its own estimate set; admitted membership is
		// checked later against the candidate snapshot at the bundle boundary.
		candidates := make([]ExecutionCandidate, len(v.Estimates))
		for i, e := range v.Estimates {
			candidates[i].ID = e.CandidateID
		}
		if err := v.EstimatorPartition.ValidateAgainst(candidates, v.Estimates); err != nil {
			return err
		}
		found := false
		for _, provenance := range v.EvaluatorVersions {
			if provenance.Name == "fitness-estimator" {
				found = true
				if provenance.Version != v.EstimatorPartition.EstimatorVersion {
					return fail(EstimatorVersionMismatch, "fitness-estimator provenance differs from partition")
				}
			}
		}
		if !found {
			return fail(EstimatorVersionMismatch, "partition requires fitness-estimator provenance")
		}
	}
	return nil
}

// ValidateAgainst checks the total-order/abstention sum type against eligibility.
func (v SelectionResult) ValidateAgainst(candidates []ExecutionCandidate) error {
	if err := validateFields(v); err != nil {
		return err
	}
	if v.Abstention != nil {
		if len(v.Order) != 0 || v.Abstention.Reason == "" {
			return fail("routing_invalid_selection", "abstention must have one reason and no order")
		}
		return nil
	}
	if len(v.Order) != len(candidates) {
		return fail("routing_invalid_selection", "order must cover all eligible candidates")
	}
	eligible := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		if eligible[c.ID] {
			return fail("routing_invalid_selection", "duplicate eligible candidate")
		}
		eligible[c.ID] = true
	}
	for i, ranked := range v.Order {
		if ranked.Position != int64(i) || !eligible[ranked.CandidateID] {
			return fail("routing_invalid_selection", "order must have unique eligible ids and positions 0..n-1")
		}
		delete(eligible, ranked.CandidateID)
	}
	return checkBasePositions(v.Order, true)
}

// checkBasePositions enforces all-or-none presence and unique non-negative
// values. A full selection order must also be a permutation of 0..n-1;
// decision alternatives may omit the selected candidate, so they need not be.
func checkBasePositions(ranked []RankedCandidate, permutation bool) error {
	present := false
	for _, candidate := range ranked {
		present = present || candidate.BasePosition != nil
	}
	if !present {
		return nil
	}
	seen := make(map[int64]bool, len(ranked))
	for _, candidate := range ranked {
		if candidate.BasePosition == nil {
			return fail(ContractBasePositionInvalid, fmt.Sprintf("candidate %q is missing base_position", candidate.CandidateID))
		}
		position := *candidate.BasePosition
		if position < 0 || seen[position] {
			return fail(ContractBasePositionInvalid, fmt.Sprintf("candidate %q base_position must be unique and non-negative", candidate.CandidateID))
		}
		if permutation && position >= int64(len(ranked)) {
			return fail(ContractBasePositionInvalid, fmt.Sprintf("candidate %q base_position must form a permutation of 0..n-1", candidate.CandidateID))
		}
		seen[position] = true
	}
	return nil
}
func (v RoutingDecision) Validate() error {
	if err := validateFields(v); err != nil {
		return err
	}
	switch v.Outcome {
	case OutcomeSelected:
		if v.SelectedCandidate == nil {
			return fail("routing_invalid_decision", "selected outcome requires selected_candidate")
		}
	case OutcomeAbstain, OutcomeNoEligibleCandidates, OutcomeTimeout, OutcomeInvalidResponse:
		if v.SelectedCandidate != nil {
			return fail("routing_invalid_decision", "non-selected outcome forbids selected_candidate")
		}
	default:
		return fail("routing_unknown_enum", "unknown outcome")
	}
	switch v.SelectionOrigin {
	case OriginRouter, OriginBaseline, OriginManual, OriginBaselineAfterAbstain:
	default:
		return fail("routing_unknown_enum", "unknown selection_origin")
	}
	if v.SelectionOrigin == OriginBaselineAfterAbstain && v.Outcome != OutcomeSelected {
		return fail("routing_invalid_decision", "baseline_after_abstain requires selected outcome")
	}
	if !timestampValid(v.PreparedAt) || (v.ExpiresAt != nil && (!timestampValid(*v.ExpiresAt) || *v.ExpiresAt <= v.PreparedAt)) {
		return fail("routing_invalid_decision", "prepared_at/expires_at must be supported timestamps with expires_at > prepared_at")
	}
	if v.SelectedCandidate != nil {
		if err := validateCandidate(*v.SelectedCandidate); err != nil {
			return err
		}
	}
	if err := checkAddresses(v.EvidenceRefs, true); err != nil {
		return err
	}
	// TODO(decision): alternatives retain increasing current positions (not candidate
	// id order); reason-code sets use lexical order. Their spec declares no key.
	for i, alternative := range v.Alternatives {
		if alternative.Position < 0 {
			return fail("routing_invalid_decision", "alternative position must be nonnegative")
		}
		if i > 0 && v.Alternatives[i-1].Position >= alternative.Position {
			code := canonical.Unordered
			if v.Alternatives[i-1].Position == alternative.Position {
				code = canonical.DuplicateKey
			}
			return &canonical.Error{Code: code, Message: "alternatives must have increasing positions"}
		}
		if err := canonical.CheckOrdered(alternative.ReasonCodes, func(v ReasonCode) string { return string(v) }); err != nil {
			return err
		}
	}
	if err := canonical.CheckOrdered(v.ReasonCodes, func(v ReasonCode) string { return string(v) }); err != nil {
		return err
	}
	return checkBasePositions(v.Alternatives, false)
}
