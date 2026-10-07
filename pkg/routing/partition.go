package routing

import "github.com/relux-works/curator-model-router/pkg/canonical"

const (
	EstimatorPartitionSchemaVersion = "estimator-partition-v1"
	FitnessEstimatorVersion         = "suitability-bp-v1"
	InvalidEstimatorPartition       = "routing_invalid_estimator_partition"
	EstimatorVersionMismatch        = "routing_estimator_version_mismatch"
	HeadroomPartitionRequired       = "headroom_partition_required"
)

// EstimatorPartition binds opaque, disjoint groups supplied by the estimator.
// Routing consumes membership; it never infers groups from scores or labels.
type EstimatorPartition struct {
	SchemaVersion    string                    `json:"schema_version"`
	EstimatorVersion string                    `json:"estimator_version"`
	Groups           []EstimatorPartitionGroup `json:"groups"`
}

type EstimatorPartitionGroup struct {
	ID           string   `json:"id"`
	CandidateIDs []string `json:"candidate_ids"`
}

// Validate checks structure without sorting, repairing or interpreting labels.
func (v EstimatorPartition) Validate() error {
	if err := validateFields(v); err != nil {
		return err
	}
	if v.SchemaVersion != EstimatorPartitionSchemaVersion {
		return fail(InvalidEstimatorPartition, "unsupported estimator partition schema")
	}
	if err := canonical.CheckOrdered(v.Groups, func(g EstimatorPartitionGroup) string { return g.ID }); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, g := range v.Groups {
		if len(g.CandidateIDs) == 0 {
			return fail(InvalidEstimatorPartition, "partition groups must be nonempty")
		}
		if err := canonical.CheckOrdered(g.CandidateIDs, func(id string) string { return id }); err != nil {
			return err
		}
		for _, id := range g.CandidateIDs {
			if seen[id] {
				return fail(InvalidEstimatorPartition, "candidate occurs in multiple partition groups")
			}
			seen[id] = true
		}
	}
	return nil
}

// ValidateAgainst checks exhaustive membership and a bijective estimate set.
// C2 uses InvalidEstimatorPartition for set mismatches; the dedicated estimate
// set refusal belongs to the separate quality/cost contract change.
func (v EstimatorPartition) ValidateAgainst(candidates []ExecutionCandidate, estimates []Estimate) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if candidates == nil || estimates == nil {
		return fail(ContractMissingField, "candidates and estimates are required; use [] for known-empty")
	}
	// Empty ids are structural; check them before any ordering comparison.
	for _, c := range candidates {
		if c.ID == "" {
			return fail(ContractEmptyField, "candidate id is empty")
		}
	}
	for _, e := range estimates {
		if e.CandidateID == "" {
			return fail(ContractEmptyField, "estimate candidate_id is empty")
		}
	}
	if err := canonical.CheckOrdered(candidates, func(c ExecutionCandidate) string { return c.ID }); err != nil {
		return err
	}
	if err := canonical.CheckOrdered(estimates, func(e Estimate) string { return e.CandidateID }); err != nil {
		return err
	}
	ids := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		if c.ID == "" {
			return fail(ContractEmptyField, "candidate id is empty")
		}
		ids[c.ID] = false
	}
	for _, g := range v.Groups {
		for _, id := range g.CandidateIDs {
			if _, ok := ids[id]; !ok {
				return fail(InvalidEstimatorPartition, "partition contains an extra candidate")
			}
			ids[id] = true
		}
	}
	for _, c := range candidates {
		if !ids[c.ID] {
			return fail(InvalidEstimatorPartition, "partition is missing a candidate")
		}
	}
	if len(estimates) != len(candidates) {
		return fail(InvalidEstimatorPartition, "estimates must cover candidates exactly once")
	}
	for i, e := range estimates {
		if err := e.Validate(); err != nil {
			return err
		}
		if e.CandidateID != candidates[i].ID {
			return fail(InvalidEstimatorPartition, "estimate candidate set differs from admitted candidates")
		}
	}
	if v.EstimatorVersion != FitnessEstimatorVersion {
		return fail(EstimatorVersionMismatch, "unsupported partition estimator version")
	}
	for _, e := range estimates {
		if e.EstimatorVersion != v.EstimatorVersion {
			return fail(EstimatorVersionMismatch, "estimate version differs from partition")
		}
	}
	return nil
}

// ValidateAgainst adds candidate membership to the evaluation's local checks.
// An absent partition preserves the historical evaluation contract.
func (v EvaluationSnapshot) ValidateAgainst(candidates []ExecutionCandidate) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.Costs != nil {
		if err := v.Costs.ValidateAgainst(candidates); err != nil {
			return err
		}
	}
	if v.EstimatorPartition != nil {
		return v.EstimatorPartition.ValidateAgainst(candidates, v.Estimates)
	}
	return nil
}

func validatePartitionBoundary(e EvaluationSnapshot, candidates []ExecutionCandidate, estimatorVersion string) error {
	if e.EstimatorPartition == nil {
		return nil
	}
	if err := e.ValidateAgainst(candidates); err != nil {
		return err
	}
	// An empty required string is structural (contract appendix rule 1), so it
	// keeps contract_empty_field before any version comparison.
	if estimatorVersion == "" {
		return fail(ContractEmptyField, `$.versions.estimator_version is empty; use "unknown" for an unknown required string`)
	}
	if estimatorVersion != e.EstimatorPartition.EstimatorVersion {
		return fail(EstimatorVersionMismatch, "bundle estimator version differs from partition")
	}
	return nil
}
