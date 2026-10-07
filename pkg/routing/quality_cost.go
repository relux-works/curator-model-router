package routing

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

const (
	QualitySelectorVersion        = "router-quality-v1"
	QualityScale                  = "role-utility-bp-v1"
	CostComparatorVersion         = "billing-lex-v1"
	CostsSchemaVersion            = "routing-costs-v1"
	QualityPolicyRequired         = "routing_quality_policy_required"
	QualityVersionMismatch        = "routing_quality_version_mismatch"
	EstimateSetMismatch           = "routing_estimate_set_mismatch"
	CostPolicyRequired            = "routing_cost_policy_required"
	InvalidCost                   = "routing_invalid_cost"
	CostVersionMismatch           = "routing_cost_version_mismatch"
	HeadroomQualityFloorConflict  = "headroom_quality_floor_conflict"
	HeadroomCostKnownnessConflict = "headroom_cost_knownness_conflict"
)

// Both thresholds are explicit values, including zero. Strict byte loaders
// require both keys; programmatic construction supplies the two integer values.
type QualityPolicy struct {
	RubricID          string `json:"rubric_id"`
	RubricVersion     string `json:"rubric_version"`
	EstimatorVersion  string `json:"estimator_version"`
	Scale             string `json:"scale"`
	MinimumFitnessBP  int64  `json:"minimum_fitness_bp"`
	MinimumCoverageBP int64  `json:"minimum_coverage_bp"`
}
type CostPolicy struct {
	Version      string         `json:"version"`
	ModelVersion string         `json:"model_version"`
	BillingOrder []BillingClass `json:"billing_order"`
}
type CandidateCost struct {
	CandidateID       string     `json:"candidate_id"`
	APIUSDMicros      *int64     `json:"api_usd_micros,omitempty"`
	QuotaDemandBP     *int64     `json:"quota_demand_bp,omitempty"`
	QuotaUnit         *string    `json:"quota_unit,omitempty"`
	LatencyMS         *int64     `json:"latency_ms,omitempty"`
	EngineOccupancyMS *int64     `json:"engine_occupancy_ms,omitempty"`
	StartMS           *int64     `json:"start_ms,omitempty"`
	Source            Provenance `json:"source"`
}
type CostSnapshot struct {
	SchemaVersion string          `json:"schema_version"`
	ModelVersion  string          `json:"model_version"`
	Candidates    []CandidateCost `json:"candidates"`
}

func (q QualityPolicy) Validate() error {
	if err := validateFields(q); err != nil {
		return err
	}
	if q.RubricID == Unknown || q.RubricVersion == Unknown || q.EstimatorVersion != FitnessEstimatorVersion || q.Scale != QualityScale {
		return fail(QualityVersionMismatch, "floor requires a known rubric and supported estimator/scale")
	}
	if q.MinimumFitnessBP < 0 || q.MinimumFitnessBP > 10000 || q.MinimumCoverageBP < 0 || q.MinimumCoverageBP > 10000 {
		return fail("routing_invalid_policy", "quality thresholds must be integer bp in 0..10000")
	}
	return nil
}
func (c CostPolicy) Validate() error {
	if err := validateFields(c); err != nil {
		return err
	}
	if c.Version != CostComparatorVersion || c.ModelVersion == Unknown {
		return fail(CostVersionMismatch, "unsupported comparator or unknown cost model")
	}
	if len(c.BillingOrder) == 0 {
		return nil
	}
	if len(c.BillingOrder) != 3 {
		return fail("routing_invalid_policy", "billing_order must be empty or an exact billing-class permutation")
	}
	seen := map[BillingClass]bool{}
	for _, class := range c.BillingOrder {
		switch class {
		case BillingLocal, BillingSubscription, BillingMetered:
		default:
			return fail("routing_unknown_enum", "unknown billing_order class")
		}
		if seen[class] {
			return fail("routing_invalid_policy", "billing_order duplicates a class")
		}
		seen[class] = true
	}
	return nil
}
func (p RoutingPolicy) validateQualityCost() error {
	switch p.Strategy {
	case StrategyConfigOrder:
		if p.Quality != nil || p.Cost != nil {
			return fail("routing_invalid_policy", "config-order forbids quality and cost policies")
		}
	case StrategyQualityFirst, StrategyCostWithQualityFloor:
		if p.Quality == nil {
			return fail(QualityPolicyRequired, "quality strategy requires explicit thresholds")
		}
		if err := p.Quality.Validate(); err != nil {
			return err
		}
		if p.Strategy == StrategyQualityFirst && p.Cost != nil {
			return fail("routing_invalid_policy", "quality-first forbids a cost policy")
		}
		if p.Strategy == StrategyCostWithQualityFloor {
			if p.Cost == nil {
				return fail(CostPolicyRequired, "cost-with-quality-floor requires a cost policy")
			}
			return p.Cost.Validate()
		}
	}
	return nil
}
func (c CandidateCost) Validate() error {
	if err := validateFields(c); err != nil {
		return err
	}
	if c.Source.Name == Unknown || c.Source.Version == "" || c.Source.Version == Unknown || !validDigest(c.Source.Digest) {
		return fail(InvalidCost, "cost source requires public name/version and strict digest")
	}
	for _, n := range []*int64{c.APIUSDMicros, c.QuotaDemandBP, c.LatencyMS, c.EngineOccupancyMS, c.StartMS} {
		if n != nil && (*n < 0 || *n > 1000000000000000) {
			return fail(InvalidCost, "cost quantities must be in 0..10^15")
		}
	}
	if (c.QuotaDemandBP == nil) != (c.QuotaUnit == nil) || (c.QuotaUnit != nil && *c.QuotaUnit == Unknown) {
		return fail(InvalidCost, "quota demand requires exactly one known quota_unit")
	}
	return nil
}
func (c CostSnapshot) Validate() error {
	if err := validateFields(c); err != nil {
		return err
	}
	if c.SchemaVersion != CostsSchemaVersion || c.ModelVersion == Unknown {
		return fail(CostVersionMismatch, "unsupported costs schema or unknown model")
	}
	if err := canonical.CheckOrdered(c.Candidates, func(c CandidateCost) string { return c.CandidateID }); err != nil {
		return err
	}
	for _, row := range c.Candidates {
		if err := row.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (c CostSnapshot) ValidateAgainst(candidates []ExecutionCandidate) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if len(c.Candidates) != len(candidates) {
		return fail(InvalidCost, "cost rows must cover admitted candidates exactly")
	}
	for i, row := range c.Candidates {
		candidate := candidates[i]
		if row.CandidateID != candidate.ID {
			return fail(InvalidCost, "cost row candidate set differs from admitted candidates")
		}
		invalid := false
		switch candidate.BillingClass {
		case BillingMetered:
			invalid = row.QuotaDemandBP != nil || row.QuotaUnit != nil || row.EngineOccupancyMS != nil || row.StartMS != nil
		case BillingSubscription:
			invalid = row.APIUSDMicros != nil || row.EngineOccupancyMS != nil || row.StartMS != nil
		case BillingLocal:
			invalid = row.APIUSDMicros != nil || row.QuotaDemandBP != nil || row.QuotaUnit != nil
		default:
			return fail("routing_unknown_enum", "unknown candidate billing class")
		}
		if invalid {
			return fail(InvalidCost, "cost row contains class-inapplicable quantities")
		}
	}
	return nil
}

// validateQualitySelectorInput is the single router-quality-v1 input boundary.
// Factories pass nil snapshots to validate the policy; every evaluating path
// supplies both snapshots before calling any base. Empty admission precedes
// evaluation checks. Structural checks precede the admitted estimate bijection,
// which precedes partition membership, floor bindings and cost-row bindings.
func validateQualitySelectorInput(s *CandidateSnapshot, e *EvaluationSnapshot, p RoutingPolicy, version string, estimatorVersion *string) error {
	if version != QualitySelectorVersion {
		return nil // Historical selectors retain their existing checks and precedence.
	}
	if s != nil {
		if err := s.Validate(); err != nil {
			return err
		}
	}
	if err := p.ValidateForVersion(QualitySelectorVersion); err != nil {
		return err
	}
	if s == nil || e == nil || len(s.Candidates) == 0 {
		return nil
	}
	if err := e.validateContent(); err != nil {
		return err
	}
	if err := validateEstimateSet(s.Candidates, e.Estimates); err != nil {
		return err
	}
	if err := e.validatePartition(); err != nil {
		return err
	}
	if p.Quality != nil {
		q := p.Quality
		for _, estimate := range e.Estimates {
			if estimate.EstimatorVersion != q.EstimatorVersion {
				return fail(QualityVersionMismatch, "estimate version differs from floor")
			}
		}
		if estimatorVersion != nil && *estimatorVersion == "" {
			return fail(ContractEmptyField, "$.versions.estimator_version is empty; use unknown for an unknown required string")
		}
		if estimatorVersion != nil && *estimatorVersion != q.EstimatorVersion {
			return fail(QualityVersionMismatch, "bundle estimator differs from floor")
		}
		if e.Rubric == nil || e.Rubric.ID != q.RubricID || e.Rubric.Version != q.RubricVersion {
			return fail(QualityVersionMismatch, "floor differs from frozen rubric")
		}
		found := false
		for _, version := range e.EvaluatorVersions {
			if version.Name == "fitness-estimator" {
				found = version.Version == q.EstimatorVersion
			}
		}
		if !found {
			return fail(QualityVersionMismatch, "floor requires matching fitness-estimator provenance")
		}
		required, err := canonical.MarshalRecord(e.Rubric.Requirements)
		if err != nil {
			return err
		}
		for _, assessment := range e.Assessments {
			projected, err := canonical.MarshalRecord(assessment.Requirements)
			if err != nil {
				return err
			}
			if !bytes.Equal(required, projected) {
				return fail(QualityVersionMismatch, "assessment requirements differ from frozen rubric projection")
			}
		}
	}
	if e.Costs != nil {
		if err := e.Costs.ValidateAgainst(s.Candidates); err != nil {
			return err
		}
	}
	if p.Strategy == StrategyCostWithQualityFloor {
		if e.Costs == nil {
			return fail(CostPolicyRequired, "cost strategy requires a costs snapshot")
		}
		if p.Cost.ModelVersion != e.Costs.ModelVersion {
			return fail(CostVersionMismatch, "cost model differs from policy")
		}
	}
	if e.EstimatorPartition != nil && estimatorVersion != nil {
		if *estimatorVersion == "" {
			return fail(ContractEmptyField, "$.versions.estimator_version is empty; use unknown for an unknown required string")
		}
		if *estimatorVersion != e.EstimatorPartition.EstimatorVersion {
			return fail(EstimatorVersionMismatch, "bundle estimator version differs from partition")
		}
	}
	return nil
}

type QualityExplanation struct {
	PassesFloor bool         `json:"passes_floor"`
	ReasonCodes []ReasonCode `json:"reason_codes"`
}
type CostExplanation struct {
	Estimate       CandidateCost `json:"estimate"`
	Complete       bool          `json:"complete"`
	ComparisonUnit *string       `json:"comparison_unit,omitempty"`
}

func qualification(e Estimate, q *QualityPolicy) QualityExplanation {
	x := QualityExplanation{PassesFloor: true, ReasonCodes: []ReasonCode{ReasonQualityFloorApplied}}
	if e.Fitness == nil {
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonFitnessUnknown)
		x.PassesFloor = false
	} else if *e.Fitness < q.MinimumFitnessBP {
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonQualityBelowFloor)
		x.PassesFloor = false
	}
	if e.Coverage == nil || *e.Coverage < q.MinimumCoverageBP {
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonCoverageBelowFloor)
		x.PassesFloor = false
	}
	return x
}
func costTuple(class BillingClass, row CandidateCost) ([]int64, string, bool) {
	fields := []*int64{}
	unit := ""
	switch class {
	case BillingMetered:
		fields = []*int64{row.APIUSDMicros, row.LatencyMS}
		unit = "usd-micros"
	case BillingSubscription:
		if row.QuotaUnit == nil {
			return nil, "", false
		}
		fields = []*int64{row.QuotaDemandBP, row.LatencyMS}
		unit = *row.QuotaUnit
	case BillingLocal:
		fields = []*int64{row.EngineOccupancyMS, row.StartMS, row.LatencyMS}
		unit = "milliseconds"
	default:
		return nil, "", false
	}
	tuple := make([]int64, len(fields))
	for i, field := range fields {
		if field == nil {
			return nil, "", false
		}
		tuple[i] = *field
	}
	return tuple, unit, true
}
func compareQuality(a, b Estimate) int {
	if n := cmp.Compare(*b.Fitness, *a.Fitness); n != 0 {
		return n
	}
	if n := cmp.Compare(*b.Coverage, *a.Coverage); n != 0 {
		return n
	}
	return cmp.Compare(a.CandidateID, b.CandidateID)
}

// QualityFirstStrategy and CostWithQualityFloorStrategy consume frozen inputs
// only. No admission, host telemetry, currency conversion or I/O occurs here.
type QualityFirstStrategy struct{}
type CostWithQualityFloorStrategy struct{}

// qualityConfigOrderStrategy preserves config ordering while enforcing the
// new selector's entire evaluation contract.
type qualityConfigOrderStrategy struct{}

func (qualityConfigOrderStrategy) selectorVersion() string   { return QualitySelectorVersion }
func (QualityFirstStrategy) selectorVersion() string         { return QualitySelectorVersion }
func (CostWithQualityFloorStrategy) selectorVersion() string { return QualitySelectorVersion }

func (qualityConfigOrderStrategy) Select(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, t TaskEnvelope) (SelectionResult, error) {
	if err := validateQualitySelectorInput(&s, &e, p, QualitySelectorVersion, nil); err != nil {
		return SelectionResult{}, err
	}
	return (ConfigOrderStrategy{}).Select(s, e, p, t)
}

func (QualityFirstStrategy) Select(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, _ TaskEnvelope) (SelectionResult, error) {
	return selectQuality(s, e, p, StrategyQualityFirst)
}
func (CostWithQualityFloorStrategy) Select(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, _ TaskEnvelope) (SelectionResult, error) {
	return selectQuality(s, e, p, StrategyCostWithQualityFloor)
}
func selectQuality(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, strategy StrategyName) (SelectionResult, error) {
	if err := validateQualitySelectorInput(&s, &e, p, QualitySelectorVersion, nil); err != nil {
		return SelectionResult{}, err
	}
	if p.Strategy != strategy {
		return SelectionResult{}, fail("routing_invalid_policy", "strategy and policy differ")
	}
	if len(s.Candidates) == 0 {
		return SelectionResult{Order: []RankedCandidate{}}, nil
	}
	passing, tail := []int{}, []int{}
	codes := make([][]ReasonCode, len(s.Candidates))
	for i, estimate := range e.Estimates {
		q := qualification(estimate, p.Quality)
		codes[i] = q.ReasonCodes
		if strategy == StrategyCostWithQualityFloor {
			if _, _, complete := costTuple(s.Candidates[i].BillingClass, e.Costs.Candidates[i]); !complete {
				codes[i] = addReason(codes[i], ReasonCostUnknown)
			}
		}
		if q.PassesFloor {
			passing = append(passing, i)
		} else {
			tail = append(tail, i)
		}
	}
	if len(passing) == 0 {
		return SelectionResult{Abstention: &Abstention{ReasonQualityFloorUnmet}}, nil
	}
	qualityCompare := func(i, j int) int { return compareQuality(e.Estimates[i], e.Estimates[j]) }
	orderedReason := ReasonQualityFirstOrdered
	if strategy == StrategyQualityFirst {
		slices.SortFunc(passing, qualityCompare)
	} else {
		orderedReason = ReasonCostWithQualityFloorOrdered
		complete, unknown := []int{}, []int{}
		tuples := make(map[int][]int64)
		classes := map[BillingClass]bool{}
		subscriptionUnit := ""
		for _, i := range passing {
			tuple, unit, known := costTuple(s.Candidates[i].BillingClass, e.Costs.Candidates[i])
			if !known {
				unknown = append(unknown, i)
				codes[i] = addReason(codes[i], ReasonCostUnknown)
				continue
			}
			class := s.Candidates[i].BillingClass
			if class == BillingSubscription {
				if subscriptionUnit != "" && subscriptionUnit != unit {
					return SelectionResult{Abstention: &Abstention{ReasonCostIncomparable}}, nil
				}
				subscriptionUnit = unit
			}
			classes[class] = true
			tuples[i] = tuple
			complete = append(complete, i)
		}
		if len(complete) == 0 {
			return SelectionResult{Abstention: &Abstention{ReasonCostUnknown}}, nil
		}
		if len(classes) > 1 && len(p.Cost.BillingOrder) == 0 {
			return SelectionResult{Abstention: &Abstention{ReasonCostIncomparable}}, nil
		}
		slices.SortFunc(complete, func(i, j int) int {
			if n := cmp.Compare(slices.Index(p.Cost.BillingOrder, s.Candidates[i].BillingClass), slices.Index(p.Cost.BillingOrder, s.Candidates[j].BillingClass)); n != 0 {
				return n
			}
			if n := slices.Compare(tuples[i], tuples[j]); n != 0 {
				return n
			}
			return qualityCompare(i, j)
		})
		slices.SortFunc(unknown, qualityCompare)
		passing = append(complete, unknown...)
	}
	r := SelectionResult{Order: make([]RankedCandidate, 0, len(s.Candidates))}
	for _, i := range append(passing, tail...) {
		r.Order = append(r.Order, RankedCandidate{CandidateID: s.Candidates[i].ID, Position: int64(len(r.Order)), ReasonCodes: addReason(codes[i], orderedReason)})
	}
	return r, r.ValidateAgainst(s.Candidates)
}

// StrategyForVersion leaves the historical Strategy API and selector constant
// intact while providing explicit dispatch for new decisions and explanations.
func StrategyForVersion(p RoutingPolicy, version string) (SelectionStrategy, error) {
	if err := validateQualitySelectorInput(nil, nil, p, version, nil); err != nil {
		return nil, err
	}
	if version == SelectorVersion {
		if p.Quality != nil || p.Cost != nil {
			return nil, fail("routing_selector_version_mismatch", "quality/cost policies require router-quality-v1")
		}
		return Strategy(p)
	}
	if version != QualitySelectorVersion {
		return nil, fail("routing_selector_version_mismatch", "unsupported selector version")
	}
	var base SelectionStrategy
	switch p.Strategy {
	case StrategyConfigOrder:
		base = qualityConfigOrderStrategy{}
	case StrategyQualityFirst:
		base = QualityFirstStrategy{}
	case StrategyCostWithQualityFloor:
		base = CostWithQualityFloorStrategy{}
	}
	if p.Headroom.Enabled {
		base = qualityHeadroomStrategy{Base: base}
	}
	return base, nil
}

// validateQualityHeadroomComposition consumes a validated base result and frozen
// inputs. Base abstention and empty admission precede wrapper requirements. Base
// comparators do not call this validator: the checks belong to composition only.
func validateQualityHeadroomComposition(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, base SelectionResult) error {
	if base.Abstention != nil || len(base.Order) == 0 {
		return nil
	}
	if p.Headroom.Equivalence == FitnessBand && e.EstimatorPartition == nil {
		return fail(HeadroomPartitionRequired, "fitness-band requires an estimator partition")
	}
	if p.Quality == nil {
		return nil
	}
	partitionGroups := map[string]string{}
	if p.Headroom.Equivalence == FitnessBand {
		for _, group := range e.EstimatorPartition.Groups {
			for _, id := range group.CandidateIDs {
				partitionGroups[id] = group.ID
			}
		}
	}
	groups := [][]int{}
	groupIDs := []string{}
	for _, ranked := range base.Order {
		i := slices.IndexFunc(s.Candidates, func(c ExecutionCandidate) bool { return c.ID == ranked.CandidateID })
		c := s.Candidates[i]
		if c.BillingClass != BillingSubscription {
			continue
		}
		group := groupFor(c, p.Headroom)
		if p.Headroom.Equivalence == FitnessBand {
			group = partitionGroups[c.ID]
		}
		if group == "" {
			continue
		}
		j := slices.Index(groupIDs, group)
		if j < 0 {
			j = len(groups)
			groupIDs = append(groupIDs, group)
			groups = append(groups, []int{})
		}
		groups[j] = append(groups[j], i)
	}
	for _, members := range groups {
		if err := guardQualityGroup(members, s, e, p); err != nil {
			return err
		}
	}
	return nil
}

func guardQualityGroup(members []int, s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy) error {
	if len(members) < 2 {
		return nil
	}
	var pass, complete bool
	for n, i := range members {
		currentPass := qualification(e.Estimates[i], p.Quality).PassesFloor
		currentComplete := false
		if p.Strategy == StrategyCostWithQualityFloor {
			_, _, currentComplete = costTuple(s.Candidates[i].BillingClass, e.Costs.Candidates[i])
		}
		if n > 0 && pass != currentPass {
			return fail(HeadroomQualityFloorConflict, "movable subscriptions straddle the quality floor")
		}
		if n > 0 && pass && p.Strategy == StrategyCostWithQualityFloor && complete != currentComplete {
			return fail(HeadroomCostKnownnessConflict, "passing movable subscriptions mix known and unknown cost")
		}
		pass, complete = currentPass, currentComplete
	}
	return nil
}

// validateEstimateSet binds ordered, structurally validated estimates to admission
// before partition membership checks can obscure the dedicated refusal.
func validateEstimateSet(candidates []ExecutionCandidate, estimates []Estimate) error {
	if len(estimates) != len(candidates) {
		return fail(EstimateSetMismatch, "estimates must cover admitted candidates exactly")
	}
	for i, estimate := range estimates {
		if estimate.CandidateID != candidates[i].ID {
			return fail(EstimateSetMismatch, "estimate set differs from admitted candidates")
		}
	}
	return nil
}

func validateBundleBoundary(b DecisionBundle) error {
	if err := validateQualitySelectorInput(&b.Snapshot, &b.Evaluation, b.Policy, b.Versions.SelectorVersion, &b.Versions.EstimatorVersion); err != nil {
		return err
	}
	if b.Versions.SelectorVersion != QualitySelectorVersion {
		if b.Policy.Quality != nil || b.Policy.Cost != nil {
			return fail("routing_selector_version_mismatch", "quality/cost policy requires router-quality-v1")
		}
		if b.Evaluation.Costs != nil {
			if err := b.Evaluation.Costs.ValidateAgainst(b.Snapshot.Candidates); err != nil {
				return err
			}
		}
		return validatePartitionBoundary(b.Evaluation, b.Snapshot.Candidates, b.Versions.EstimatorVersion)
	}
	return nil
}

func enrichQualityExplanation(ex Explanation, b DecisionBundle) Explanation {
	if len(ex.Candidates) == 0 && len(b.Snapshot.Candidates) > 0 {
		for i, id := range b.Snapshot.ConfigOrder {
			for _, candidate := range b.Snapshot.Candidates {
				if candidate.ID != id {
					continue
				}
				x := quantities(candidate, int64(i), b.Snapshot, b.Policy.Headroom, b.Envelope.Role)
				x.Group = ""
				ex.Candidates = append(ex.Candidates, x)
			}
		}
	}
	for i := range ex.Candidates {
		x := &ex.Candidates[i]
		for _, estimate := range b.Evaluation.Estimates {
			if estimate.CandidateID != x.CandidateID {
				continue
			}
			copy := estimate
			copy.ContributingRecordIDs = slices.Clone(estimate.ContributingRecordIDs)
			if estimate.Fitness != nil {
				copy.Fitness = ptr64(*estimate.Fitness)
			}
			if estimate.Coverage != nil {
				copy.Coverage = ptr64(*estimate.Coverage)
			}
			x.Estimate = &copy
			if b.Policy.Quality != nil {
				q := qualification(copy, b.Policy.Quality)
				x.Quality = &q
				for _, code := range q.ReasonCodes {
					x.ReasonCodes = addReason(x.ReasonCodes, code)
				}
			}
		}
		if b.Evaluation.Costs != nil {
			for _, row := range b.Evaluation.Costs.Candidates {
				if row.CandidateID != x.CandidateID {
					continue
				}
				copy := row
				clone := func(v *int64) *int64 {
					if v == nil {
						return nil
					}
					return ptr64(*v)
				}
				copy.APIUSDMicros, copy.QuotaDemandBP = clone(row.APIUSDMicros), clone(row.QuotaDemandBP)
				copy.LatencyMS, copy.EngineOccupancyMS, copy.StartMS = clone(row.LatencyMS), clone(row.EngineOccupancyMS), clone(row.StartMS)
				if row.QuotaUnit != nil {
					unit := *row.QuotaUnit
					copy.QuotaUnit = &unit
				}
				_, unit, complete := costTuple(x.BillingClass, copy)
				x.Cost = &CostExplanation{Estimate: copy, Complete: complete}
				if complete {
					x.Cost.ComparisonUnit = &unit
				} else if b.Policy.Strategy == StrategyCostWithQualityFloor {
					x.ReasonCodes = addReason(x.ReasonCodes, ReasonCostUnknown)
				}
			}
		}
		if ex.Abstention != nil {
			x.ReasonCodes = addReason(x.ReasonCodes, ex.Abstention.Reason)
		}
	}
	return ex
}

// ValidateForVersion adds required floor/cost checks for the quality selector.
// Validate/Digest retain floorless historical policy bytes, including the old
// strategy-name fixtures whose implementations were refused by router-core-v1.
func (p RoutingPolicy) ValidateForVersion(version string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	switch version {
	case SelectorVersion:
		if p.Quality != nil || p.Cost != nil {
			return fail("routing_selector_version_mismatch", "quality/cost policy requires router-quality-v1")
		}
		return nil
	case QualitySelectorVersion:
		return p.validateQualityCost()
	default:
		return fail("routing_selector_version_mismatch", "unsupported selector version")
	}
}
