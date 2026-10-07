package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func qualityBundle(n int, strategy StrategyName) DecisionBundle {
	b := fixtureBundle(n)
	b.Policy.Headroom.Enabled = false
	b.Policy.Strategy = strategy
	b.Policy.Quality = &QualityPolicy{"review", "1", FitnessEstimatorVersion, QualityScale, 6000, 5000}
	b.Versions.SelectorVersion = QualitySelectorVersion
	b.Versions.EstimatorVersion = FitnessEstimatorVersion
	b.Evaluation.Rubric = &Rubric{"review", "1", Requirements{Items: []Requirement{{Category: "review.code", WeightBP: ptr64(10000)}}}}
	b.Evaluation.Assessments = []Assessment{{ID: "assessment", Requirements: b.Evaluation.Rubric.Requirements, AssessorVersion: "rules@1"}}
	b.Evaluation.EvaluatorVersions = []Provenance{{Name: "fitness-estimator", Version: FitnessEstimatorVersion}}
	for _, c := range b.Snapshot.Candidates {
		b.Evaluation.Estimates = append(b.Evaluation.Estimates, Estimate{c.ID, ptr64(7000), ptr64(5000), []string{}, FitnessEstimatorVersion})
	}
	if strategy == StrategyCostWithQualityFloor {
		b.Policy.Cost = &CostPolicy{CostComparatorVersion, "forecast@1", []BillingClass{}}
		b.Evaluation.Costs = &CostSnapshot{CostsSchemaVersion, "forecast@1", []CandidateCost{}}
		for _, c := range b.Snapshot.Candidates {
			b.Evaluation.Costs.Candidates = append(b.Evaluation.Costs.Candidates, knownCost(c.ID, BillingSubscription))
		}
	}
	return b
}
func knownCost(id string, class BillingClass) CandidateCost {
	row := CandidateCost{CandidateID: id, LatencyMS: ptr64(12), Source: Provenance{"forecast", "1", fixtureDigest}}
	switch class {
	case BillingMetered:
		row.APIUSDMicros = ptr64(10)
	case BillingSubscription:
		row.QuotaDemandBP = ptr64(10)
		row.QuotaUnit = ptrString("requests@1")
	case BillingLocal:
		row.EngineOccupancyMS = ptr64(30)
		row.StartMS = ptr64(0)
	}
	return row
}
func setBilling(b *DecisionBundle, i int, class BillingClass) {
	b.Snapshot.Candidates[i].BillingClass = class
	if b.Evaluation.Costs != nil {
		b.Evaluation.Costs.Candidates[i] = knownCost(b.Snapshot.Candidates[i].ID, class)
	}
}
func selectBundle(t *testing.T, b DecisionBundle) SelectionResult {
	t.Helper()
	strategy, err := StrategyForVersion(b.Policy, b.Versions.SelectorVersion)
	if err != nil {
		t.Fatal(err)
	}
	r, err := strategy.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateAgainst(b.Snapshot.Candidates); err != nil {
		t.Fatal(err)
	}
	return r
}
func ids(r SelectionResult) []string {
	result := []string{}
	for _, candidate := range r.Order {
		result = append(result, candidate.CandidateID)
	}
	return result
}

func TestQualityInclusiveIndependentThresholds(t *testing.T) {
	for _, f := range []int64{5999, 6000, 6001} {
		for _, c := range []int64{4999, 5000, 5001} {
			t.Run(fmt.Sprintf("F%d_C%d", f, c), func(t *testing.T) {
				b := qualityBundle(1, StrategyQualityFirst)
				b.Evaluation.Estimates[0].Fitness, b.Evaluation.Estimates[0].Coverage = ptr64(f), ptr64(c)
				r := selectBundle(t, b)
				if (r.Abstention == nil) != (f >= 6000 && c >= 5000) {
					t.Fatalf("unexpected qualification: %+v", r)
				}
			})
		}
	}
}
func TestQualityZeroVersusUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		f, c *int64
		pass bool
	}{
		{"known_zero", ptr64(0), ptr64(0), true}, {"unknown_fitness", nil, ptr64(0), false}, {"unknown_coverage", ptr64(0), nil, false}, {"both_unknown", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := qualityBundle(1, StrategyQualityFirst)
			b.Policy.Quality.MinimumFitnessBP, b.Policy.Quality.MinimumCoverageBP = 0, 0
			b.Evaluation.Estimates[0].Fitness, b.Evaluation.Estimates[0].Coverage = tc.f, tc.c
			r := selectBundle(t, b)
			if (r.Abstention == nil) != tc.pass {
				t.Fatalf("unexpected qualification: %+v", r)
			}
		})
	}
}
func TestQualityCostVectors(t *testing.T) {
	cases := []struct {
		name     string
		strategy StrategyName
		mutate   func(*DecisionBundle)
		order    []string
		abstain  ReasonCode
	}{
		{"equal_quality_reversed_config_lexical_id", StrategyQualityFirst, func(b *DecisionBundle) { b.Snapshot.ConfigOrder = []string{"b", "a"} }, []string{"a", "b"}, ""},
		{"fitness_then_coverage", StrategyQualityFirst, func(b *DecisionBundle) { b.Evaluation.Estimates[1].Coverage = ptr64(5001) }, []string{"b", "a"}, ""},
		{"cheap_below_floor_retained_in_tail", StrategyCostWithQualityFloor, func(b *DecisionBundle) {
			setBilling(b, 0, BillingMetered)
			setBilling(b, 1, BillingMetered)
			b.Evaluation.Estimates[0].Fitness = ptr64(5900)
			b.Evaluation.Costs.Candidates[0].APIUSDMicros = ptr64(1)
		}, []string{"b", "a"}, ""},
		{"all_fail_floor", StrategyQualityFirst, func(b *DecisionBundle) {
			for i := range b.Evaluation.Estimates {
				b.Evaluation.Estimates[i].Fitness = ptr64(5999)
			}
		}, nil, ReasonQualityFloorUnmet},
		{"all_passing_tuples_incomplete", StrategyCostWithQualityFloor, func(b *DecisionBundle) {
			for i := range b.Evaluation.Costs.Candidates {
				b.Evaluation.Costs.Candidates[i].LatencyMS = nil
			}
		}, nil, ReasonCostUnknown},
		{"unknown_usd_vs_explicit_zero", StrategyCostWithQualityFloor, func(b *DecisionBundle) {
			setBilling(b, 0, BillingMetered)
			setBilling(b, 1, BillingMetered)
			b.Evaluation.Costs.Candidates[0].APIUSDMicros = nil
			b.Evaluation.Costs.Candidates[1].APIUSDMicros = ptr64(0)
		}, []string{"b", "a"}, ""},
		{"local_metered_without_priority", StrategyCostWithQualityFloor, func(b *DecisionBundle) { setBilling(b, 0, BillingLocal); setBilling(b, 1, BillingMetered) }, nil, ReasonCostIncomparable},
		{"declared_metered_before_local", StrategyCostWithQualityFloor, func(b *DecisionBundle) {
			setBilling(b, 0, BillingLocal)
			setBilling(b, 1, BillingMetered)
			b.Policy.Cost.BillingOrder = []BillingClass{BillingMetered, BillingLocal, BillingSubscription}
		}, []string{"b", "a"}, ""},
		{"different_subscription_units", StrategyCostWithQualityFloor, func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[1].QuotaUnit = ptrString("tokens@1") }, nil, ReasonCostIncomparable},
		{"cold_ensurable_local_positive_start", StrategyCostWithQualityFloor, func(b *DecisionBundle) {
			setBilling(b, 0, BillingLocal)
			setBilling(b, 1, BillingLocal)
			b.Evaluation.Costs.Candidates[0].StartMS = ptr64(10)
		}, []string{"b", "a"}, ""},
		{"tuple_latency_before_quality", StrategyCostWithQualityFloor, func(b *DecisionBundle) {
			b.Evaluation.Costs.Candidates[0].LatencyMS = ptr64(13)
			b.Evaluation.Estimates[0].Fitness = ptr64(9000)
		}, []string{"b", "a"}, ""},
		{"equal_tuple_quality_then_id", StrategyCostWithQualityFloor, func(b *DecisionBundle) {
			b.Snapshot.ConfigOrder = []string{"b", "a"}
			b.Evaluation.Estimates[1].Coverage = ptr64(5001)
		}, []string{"b", "a"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := qualityBundle(2, tc.strategy)
			tc.mutate(&b)
			before, _ := json.Marshal(b)
			r := selectBundle(t, b)
			if tc.abstain != "" {
				if r.Abstention == nil || r.Abstention.Reason != tc.abstain || len(r.Order) != 0 {
					t.Fatalf("result=%+v", r)
				}
			} else if !slices.Equal(ids(r), tc.order) {
				t.Fatalf("order=%v want=%v", ids(r), tc.order)
			}
			routed, err := Route(b)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(routed.Decision.ReasonCodes, ReasonQualityFloorApplied) {
				t.Fatal("missing floor marker")
			}
			replay, err := Replay(b, routed.Decision)
			if err != nil || !replay.Equal {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
			ex, err := ExplainDecision(b, *routed.Decision)
			if err != nil {
				t.Fatal(err)
			}
			if len(ex.Candidates) != 2 {
				t.Fatalf("explanation lost candidates: %+v", ex)
			}
			for _, x := range ex.Candidates {
				if x.Estimate == nil || x.Quality == nil {
					t.Fatalf("missing quality explanation: %+v", x)
				}
			}
			after, _ := json.Marshal(b)
			if !bytes.Equal(before, after) {
				t.Fatal("selection mutated frozen inputs")
			}
		})
	}
}

// One cross-product pins every quality evaluator to the same typed refusals.
// Factories accept only policies: their returned strategies exercise evaluation;
// the versionless factory and its core-only refusals are controls below.
func TestQualityInputMatrix(t *testing.T) {
	type endpoint struct {
		name string
		call func(DecisionBundle, SelectionResult, RoutingDecision) error
	}
	endpoints := []endpoint{
		{"StrategyForVersion", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
			base, err := StrategyForVersion(b.Policy, b.Versions.SelectorVersion)
			if err != nil {
				return err
			}
			_, err = base.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			return err
		}},
		{"HeadroomStrategy/versioned_base", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
			base, err := StrategyForVersion(b.Policy, b.Versions.SelectorVersion)
			if err != nil {
				return err
			}
			_, err = (HeadroomStrategy{Base: base}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			return err
		}},
		{"HeadroomStrategy/nested_versioned_base", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
			base, err := StrategyForVersion(b.Policy, b.Versions.SelectorVersion)
			if err != nil {
				return err
			}
			inner := &HeadroomStrategy{Base: base}
			_, err = (HeadroomStrategy{Base: inner}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			return err
		}},
		{"qualityHeadroomStrategy/injected_base", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
			_, err := (qualityHeadroomStrategy{Base: ConfigOrderStrategy{}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			return err
		}},
		{"base_strategy", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
			var base SelectionStrategy = qualityConfigOrderStrategy{}
			switch b.Policy.Strategy {
			case StrategyQualityFirst:
				base = QualityFirstStrategy{}
			case StrategyCostWithQualityFloor:
				base = CostWithQualityFloorStrategy{}
			}
			_, err := base.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			return err
		}},
		{"Route", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error { _, err := Route(b); return err }},
		{"Explain", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error { _, err := Explain(b); return err }},
		{"BuildDecision", func(b DecisionBundle, r SelectionResult, _ RoutingDecision) error {
			_, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
			return err
		}},
		{"LoadBundle", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
			raw, err := json.Marshal(b)
			if err != nil {
				return err
			}
			_, err = LoadBundle(raw)
			return err
		}},
		{"Replay", func(b DecisionBundle, _ SelectionResult, d RoutingDecision) error {
			_, err := Replay(b, &d)
			return err
		}},
		{"ExplainDecision", func(b DecisionBundle, _ SelectionResult, d RoutingDecision) error {
			_, err := ExplainDecision(b, d)
			return err
		}},
	}
	invalidInputs := []struct {
		name   string
		mutate func(*DecisionBundle)
		code   string
	}{
		{"invalid_partition", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition = &EstimatorPartition{"unsupported", FitnessEstimatorVersion, []EstimatorPartitionGroup{}}
		}, InvalidEstimatorPartition},
		{"partition_membership", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition = &EstimatorPartition{EstimatorPartitionSchemaVersion, FitnessEstimatorVersion, []EstimatorPartitionGroup{{"only", []string{"a"}}}}
		}, InvalidEstimatorPartition},
		{"partition_version", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition = &EstimatorPartition{EstimatorPartitionSchemaVersion, "other", []EstimatorPartitionGroup{{"all", []string{"a", "b", "c", "local-x"}}}}
		}, EstimatorVersionMismatch},
		{"estimate_set_before_partition", func(b *DecisionBundle) {
			b.Evaluation.Estimates = b.Evaluation.Estimates[:1]
			b.Evaluation.EstimatorPartition = &EstimatorPartition{"unsupported", FitnessEstimatorVersion, []EstimatorPartitionGroup{}}
		}, EstimateSetMismatch},
		{"substituted_cost_row", func(b *DecisionBundle) {
			b.Evaluation.Costs.Candidates[len(b.Evaluation.Costs.Candidates)-1].CandidateID = "z"
		}, InvalidCost},
		{"quality_required", func(b *DecisionBundle) { b.Policy.Quality = nil }, QualityPolicyRequired},
		{"cost_policy_required", func(b *DecisionBundle) { b.Policy.Cost = nil }, CostPolicyRequired},
		{"cost_snapshot_required", func(b *DecisionBundle) { b.Evaluation.Costs = nil }, CostPolicyRequired},
		{"unsupported_scale", func(b *DecisionBundle) { b.Policy.Quality.Scale = "probability" }, QualityVersionMismatch},
		{"unknown_floor_rubric", func(b *DecisionBundle) { b.Policy.Quality.RubricVersion = Unknown }, QualityVersionMismatch},
		{"rubric_mismatch", func(b *DecisionBundle) { b.Evaluation.Rubric.ID = "other" }, QualityVersionMismatch},
		{"assessment_projection_mismatch", func(b *DecisionBundle) {
			b.Evaluation.Assessments[0].Requirements = Requirements{Items: []Requirement{{Category: "review.spec"}}}
		}, QualityVersionMismatch},
		{"estimate_version_mismatch", func(b *DecisionBundle) { b.Evaluation.Estimates[0].EstimatorVersion = "other" }, QualityVersionMismatch},
		{"provenance_version_mismatch", func(b *DecisionBundle) { b.Evaluation.EvaluatorVersions[0].Version = "other" }, QualityVersionMismatch},
		{"missing_estimator_provenance", func(b *DecisionBundle) { b.Evaluation.EvaluatorVersions = []Provenance{} }, QualityVersionMismatch},
		{"empty_bundle_version", func(b *DecisionBundle) { b.Versions.EstimatorVersion = "" }, ContractEmptyField},
		{"bundle_version_mismatch", func(b *DecisionBundle) { b.Versions.EstimatorVersion = "other" }, QualityVersionMismatch},
		{"missing_estimate", func(b *DecisionBundle) { b.Evaluation.Estimates = b.Evaluation.Estimates[:1] }, EstimateSetMismatch},
		{"extra_estimate", func(b *DecisionBundle) {
			b.Evaluation.Estimates = append(b.Evaluation.Estimates, Estimate{"z", nil, nil, []string{}, FitnessEstimatorVersion})
		}, EstimateSetMismatch},
		{"substituted_estimate", func(b *DecisionBundle) { b.Evaluation.Estimates[len(b.Evaluation.Estimates)-1].CandidateID = "z" }, EstimateSetMismatch},
		{"duplicate_estimate", func(b *DecisionBundle) { b.Evaluation.Estimates[1].CandidateID = "a" }, canonical.DuplicateKey},
		{"invalid_estimate", func(b *DecisionBundle) { b.Evaluation.Estimates[0].Fitness = ptr64(10001) }, "routing_invalid_estimate"},
		{"missing_cost_row", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates = b.Evaluation.Costs.Candidates[:1] }, InvalidCost},
		{"extra_cost_row", func(b *DecisionBundle) {
			b.Evaluation.Costs.Candidates = append(b.Evaluation.Costs.Candidates, knownCost("z", BillingSubscription))
		}, InvalidCost},
		{"duplicate_cost_row", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[1].CandidateID = "a" }, canonical.DuplicateKey},
		{"unordered_cost_rows", func(b *DecisionBundle) { slices.Reverse(b.Evaluation.Costs.Candidates) }, canonical.Unordered},
		{"negative_cost", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].LatencyMS = ptr64(-1) }, InvalidCost},
		{"too_large_cost", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].QuotaDemandBP = ptr64(1000000000000001) }, InvalidCost},
		{"class_inapplicable_cost", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].APIUSDMicros = ptr64(1) }, InvalidCost},
		{"quota_without_unit", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].QuotaUnit = nil }, InvalidCost},
		{"unit_without_demand", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].QuotaDemandBP = nil }, InvalidCost},
		{"unknown_unit", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].QuotaUnit = ptrString(Unknown) }, InvalidCost},
		{"source_version_absent", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].Source.Version = "" }, InvalidCost},
		{"source_digest_absent", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].Source.Digest = "" }, InvalidCost},
		{"bad_source_digest", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].Source.Digest = "bad" }, ContractInvalidDigest},
		{"cost_model_mismatch", func(b *DecisionBundle) { b.Evaluation.Costs.ModelVersion = "other" }, CostVersionMismatch},
		{"cost_model_unknown", func(b *DecisionBundle) { b.Policy.Cost.ModelVersion = Unknown }, CostVersionMismatch},
		{"cost_schema_unsupported", func(b *DecisionBundle) { b.Evaluation.Costs.SchemaVersion = "other" }, CostVersionMismatch},
		{"cost_comparator_unsupported", func(b *DecisionBundle) { b.Policy.Cost.Version = "other" }, CostVersionMismatch},
		{"malformed_billing_order", func(b *DecisionBundle) { b.Policy.Cost.BillingOrder = []BillingClass{BillingLocal} }, "routing_invalid_policy"},
		{"duplicate_billing_order", func(b *DecisionBundle) {
			b.Policy.Cost.BillingOrder = []BillingClass{BillingLocal, BillingLocal, BillingMetered}
		}, "routing_invalid_policy"},
		{"billing_order_unknown_class", func(b *DecisionBundle) {
			b.Policy.Cost.BillingOrder = []BillingClass{BillingLocal, BillingSubscription, "other"}
		}, "routing_unknown_enum"},
		{"floor_out_of_range", func(b *DecisionBundle) { b.Policy.Quality.MinimumFitnessBP = 10001 }, "routing_invalid_policy"},
		{"headroom_floor_conflict", func(b *DecisionBundle) { b.Evaluation.Estimates[0].Fitness = ptr64(5999) }, HeadroomQualityFloorConflict},
		{"headroom_knownness_conflict", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].LatencyMS = nil }, HeadroomCostKnownnessConflict},
	}
	for _, strategy := range []StrategyName{StrategyConfigOrder, StrategyQualityFirst, StrategyCostWithQualityFloor} {
		for _, enabled := range []bool{false, true} {
			for _, eq := range []Equivalence{SamePairAnyHome, DeclaredGroups, FitnessBand} {
				partition := eq == FitnessBand
				t.Run(fmt.Sprintf("%s/headroom_%t/%s", strategy, enabled, eq), func(t *testing.T) {
					makeBundle := func() DecisionBundle {
						b := wrapperBundle(eq, StrategyCostWithQualityFloor)
						b.Policy.Strategy = strategy
						if strategy != StrategyCostWithQualityFloor {
							b.Policy.Cost = nil
						}
						if strategy == StrategyConfigOrder {
							b.Policy.Quality = nil
						}
						b.Policy.Headroom.Enabled = enabled
						return b
					}
					b := makeBundle()
					r := selectBundle(t, b)
					d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
					if err != nil || d == nil {
						t.Fatalf("valid decision: d=%v err=%v", d, err)
					}
					assertQualityDecisionReplays(t, b, d)
					t.Run("selection_consistency", func(t *testing.T) {
						testQualitySelectionConsistency(t, makeBundle)
					})
					calls := slices.Clone(endpoints)
					if strategy != StrategyConfigOrder {
						calls = append(calls, endpoint{"HeadroomStrategy/injected_base", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
							_, err := (HeadroomStrategy{Base: ConfigOrderStrategy{}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
							return err
						}})
					}
					calls = append(calls, endpoint{"qualityHeadroomStrategy/abstaining_base", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
						_, err := (qualityHeadroomStrategy{Base: fixedStrategy{result: SelectionResult{Abstention: &Abstention{ReasonQualityFloorUnmet}}}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
						return err
					}})
					for _, api := range calls {
						t.Run("valid/"+api.name, func(t *testing.T) {
							assertCode(t, api.call(b, r, *d), "")
							if api.name == "BuildDecision" {
								assertQualityDecisionReplays(t, b, d)
							}
						})
					}
					for _, broken := range invalidInputs {
						// Binding members are required only by the strategies that consume them.
						qualityOnly := slices.Contains([]string{"quality_required", "unsupported_scale", "unknown_floor_rubric", "rubric_mismatch", "assessment_projection_mismatch", "floor_out_of_range", "headroom_floor_conflict"}, broken.name)
						costOnly := slices.Contains([]string{"cost_policy_required", "cost_snapshot_required", "cost_model_mismatch", "cost_model_unknown", "cost_comparator_unsupported", "malformed_billing_order", "duplicate_billing_order", "billing_order_unknown_class", "headroom_knownness_conflict"}, broken.name)
						if (qualityOnly && strategy == StrategyConfigOrder) || (costOnly && strategy != StrategyCostWithQualityFloor) {
							continue
						}
						versionInput := slices.Contains([]string{"estimate_version_mismatch", "provenance_version_mismatch", "missing_estimator_provenance", "empty_bundle_version", "bundle_version_mismatch"}, broken.name)
						if versionInput && strategy == StrategyConfigOrder && !partition {
							continue // No floor or partition binds the estimator version.
						}
						t.Run(broken.name, func(t *testing.T) {
							bad := makeBundle()
							broken.mutate(&bad)
							code := broken.code
							if partition && slices.Contains([]string{"estimate_version_mismatch", "provenance_version_mismatch", "missing_estimator_provenance"}, broken.name) {
								code = EstimatorVersionMismatch
							}
							if strategy == StrategyConfigOrder && broken.name == "bundle_version_mismatch" {
								code = EstimatorVersionMismatch
							}
							composition := broken.code == HeadroomQualityFloorConflict || broken.code == HeadroomCostKnownnessConflict
							inputResult, inputDecision := r, *d
							if composition && !enabled {
								// Disabled-headroom controls use the new valid base result
								// and decision. Enabled cases retain the original selection
								// to reproduce the decision-boundary bypass.
								inputResult = selectBundle(t, bad)
								accepted, err := BuildDecision(bad.Envelope, bad.Snapshot, bad.Evaluation, bad.Policy, inputResult, bad.Versions)
								if err != nil {
									t.Fatal(err)
								}
								inputDecision = *accepted
								assertQualityDecisionReplays(t, bad, accepted)
							}
							for _, api := range calls {
								if broken.name == "quality_required" && strategy == StrategyQualityFirst && api.name == "HeadroomStrategy/injected_base" {
									continue // With no floor or versioned base this is a historical wrapper.
								}
								if slices.Contains([]string{"empty_bundle_version", "bundle_version_mismatch"}, broken.name) && slices.Contains([]string{"StrategyForVersion", "HeadroomStrategy/versioned_base", "HeadroomStrategy/nested_versioned_base", "qualityHeadroomStrategy/injected_base", "base_strategy", "HeadroomStrategy/injected_base", "qualityHeadroomStrategy/abstaining_base"}, api.name) {
									continue // Select has no DecisionOptions parameter.
								}
								want := code
								if composition {
									// LoadBundle checks wire/input contracts; standalone bases
									// do not compose. Explicit wrappers run even if the policy
									// toggle is off, and injected base abstention passes through.
									plain := slices.Contains([]string{"base_strategy", "LoadBundle", "qualityHeadroomStrategy/abstaining_base"}, api.name)
									policyControlled := slices.Contains([]string{"StrategyForVersion", "Route", "Explain", "BuildDecision", "Replay", "ExplainDecision"}, api.name)
									if plain || (!enabled && policyControlled) {
										want = ""
									}
								}
								t.Run(api.name, func(t *testing.T) {
									assertCode(t, api.call(bad, inputResult, inputDecision), want)
									if api.name == "BuildDecision" && want == "" {
										accepted, err := BuildDecision(bad.Envelope, bad.Snapshot, bad.Evaluation, bad.Policy, inputResult, bad.Versions)
										if err != nil {
											t.Fatal(err)
										}
										assertQualityDecisionReplays(t, bad, accepted)
									}
								})
							}
						})
					}
				})
			}
		}
	}
	t.Run("config_order_empty_admission", func(t *testing.T) {
		b := qualityBundle(0, StrategyQualityFirst)
		b.Policy.Strategy, b.Policy.Quality = StrategyConfigOrder, nil
		b.Policy.Headroom.Equivalence = FitnessBand
		b.Evaluation.Estimates = []Estimate{{CandidateID: "ghost", Fitness: ptr64(10001)}}
		for _, enabled := range []bool{false, true} {
			b.Policy.Headroom.Enabled = enabled
			base, err := StrategyForVersion(b.Policy, QualitySelectorVersion)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []SelectionStrategy{base, HeadroomStrategy{Base: base}} {
				got, err := candidate.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
				if err != nil || len(got.Order) != 0 || got.Abstention != nil {
					t.Fatalf("empty selection changed: got=%+v err=%v", got, err)
				}
			}
		}
	})
	t.Run("config_order_base_abstention", func(t *testing.T) {
		b := wrapperBundle(FitnessBand, StrategyQualityFirst)
		b.Policy.Strategy, b.Policy.Quality = StrategyConfigOrder, nil
		b.Evaluation.EstimatorPartition = nil
		want := SelectionResult{Abstention: &Abstention{ReasonQualityFloorUnmet}}
		base := qualityHeadroomStrategy{Base: fixedStrategy{result: want}}
		for _, candidate := range []SelectionStrategy{base, HeadroomStrategy{Base: base}} {
			got, err := candidate.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("base abstention changed: got=%+v err=%v", got, err)
			}
		}
	})
	// Versionless Strategy and unversioned wrappers remain historical APIs.
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("core_controls/headroom_%t", enabled), func(t *testing.T) {
			b := wrapperBundle(FitnessBand, StrategyQualityFirst)
			b.Policy.Strategy, b.Policy.Quality = StrategyConfigOrder, nil
			b.Policy.Headroom.Enabled = enabled
			b.Versions.SelectorVersion = SelectorVersion
			r := selectBundle(t, b)
			d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
			if err != nil || d == nil {
				t.Fatalf("valid core decision: d=%v err=%v", d, err)
			}
			b.Evaluation.Estimates = b.Evaluation.Estimates[:3]
			for _, api := range append(slices.Clone(endpoints[:3]), endpoint{"Strategy", func(b DecisionBundle, _ SelectionResult, _ RoutingDecision) error {
				base, err := Strategy(b.Policy)
				if err != nil {
					return err
				}
				_, err = base.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
				return err
			}}) {
				t.Run(api.name, func(t *testing.T) {
					want := InvalidEstimatorPartition
					if !enabled && (api.name == "Strategy" || api.name == "StrategyForVersion") {
						want = ""
					}
					assertCode(t, api.call(b, r, *d), want)
				})
			}
		})
	}
	t.Run("core_goldens", func(t *testing.T) {
		t.Run("config_order", TestConfigOrderCanonicalGoldens)
		t.Run("richer_contract", TestRicherContractGoldens)
		t.Run("headroom", TestHeadroomGoldenVectors)
		t.Run("c2", TestC2Goldens)
	})
	t.Run("core_supplied_selection", testCoreBuildDecisionSuppliedSelection)
}

func TestQualityPublicAPIsRejectConfigOrderPolicies(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, member := range []string{"absent", "quality", "cost", "quality_and_cost"} {
			t.Run(fmt.Sprintf("headroom_%t/%s", enabled, member), func(t *testing.T) {
				b := qualityBundle(2, StrategyCostWithQualityFloor)
				b.Policy.Strategy = StrategyConfigOrder
				b.Policy.Headroom.Enabled = enabled
				// A usable base would select a below the supplied quality floor.
				b.Evaluation.Estimates[0].Fitness = ptr64(0)
				if member == "absent" || member == "cost" {
					b.Policy.Quality = nil
				}
				if member == "absent" || member == "quality" {
					b.Policy.Cost = nil
				}
				for _, endpoint := range []struct {
					name string
					call func() (SelectionResult, error)
				}{
					{"factory", func() (SelectionResult, error) {
						strategy, err := Strategy(b.Policy)
						if err != nil || member != "absent" {
							return SelectionResult{}, err
						}
						return strategy.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
					}},
					{"wrapper", func() (SelectionResult, error) {
						return (HeadroomStrategy{Base: ConfigOrderStrategy{}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
					}},
				} {
					t.Run(endpoint.name, func(t *testing.T) {
						r, err := endpoint.call()
						if member != "absent" {
							requireErrorCode(t, err, "routing_invalid_policy")
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						if err := r.ValidateAgainst(b.Snapshot.Candidates); err != nil {
							t.Fatal(err)
						}
					})
				}
			})
		}
		for _, strategy := range []StrategyName{StrategyQualityFirst, StrategyCostWithQualityFloor} {
			t.Run(fmt.Sprintf("headroom_%t/floorless_%s", enabled, strategy), func(t *testing.T) {
				p := DefaultPolicy("v1")
				p.Headroom.Enabled = enabled
				p.Strategy = strategy
				_, err := Strategy(p)
				requireErrorCode(t, err, "routing_strategy_not_implemented")
			})
		}
	}
}

func TestQualityStrictPolicyKeys(t *testing.T) {
	b := qualityBundle(1, StrategyQualityFirst)
	raw, _ := json.Marshal(b.Policy)
	bundleRaw, _ := json.Marshal(b)
	for _, tc := range []struct {
		name           string
		old, new, code string
	}{
		{"fitness_missing", `"minimum_fitness_bp":6000,`, "", ContractMissingField},
		{"coverage_missing", `,"minimum_coverage_bp":5000`, "", ContractMissingField},
		{"explicit_zero", `"minimum_fitness_bp":6000,"minimum_coverage_bp":5000`, `"minimum_fitness_bp":0,"minimum_coverage_bp":0`, ""},
		{"fractional_floor", `"minimum_fitness_bp":6000`, `"minimum_fitness_bp":0.9`, "routing_invalid_policy"},
		{"null_floor", `"minimum_fitness_bp":6000`, `"minimum_fitness_bp":null`, canonical.Null},
		{"wrong_case", `"minimum_fitness_bp":6000`, `"Minimum_fitness_bp":6000`, ContractUnknownField},
		{"unknown_nested_key", `"scale":"role-utility-bp-v1"`, `"scale":"role-utility-bp-v1","extra":0`, ContractUnknownField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(string(raw), tc.old, tc.new, 1)
			if changed == string(raw) {
				t.Fatal("test replacement absent")
			}
			_, err := LoadPolicy([]byte(changed))
			assertCode(t, err, tc.code)
			changedBundle := strings.Replace(string(bundleRaw), tc.old, tc.new, 1)
			if changedBundle == string(bundleRaw) {
				t.Fatal("bundle test replacement absent")
			}
			_, err = LoadBundle([]byte(changedBundle))
			assertCode(t, err, tc.code)
		})
	}
}

func wrapperBundle(equivalence Equivalence, strategy StrategyName) DecisionBundle {
	b := qualityBundle(4, strategy)
	b.Policy.Headroom.Enabled = true
	b.Policy.Headroom.Equivalence = equivalence
	b.Snapshot.Candidates[3].ID = "local-x"
	b.Evaluation.Estimates[3].CandidateID = "local-x"
	if b.Evaluation.Costs != nil {
		b.Evaluation.Costs.Candidates[3].CandidateID = "local-x"
	}
	setBilling(&b, 3, BillingLocal)
	b.Snapshot.ConfigOrder = []string{"a", "local-x", "b", "c"}
	b.Snapshot.Candidates[2].Model = "model-c"
	for i, f := range []int64{9000, 7000, 6000, 8000} {
		b.Evaluation.Estimates[i].Fitness = ptr64(f)
	}
	b.Snapshot.UsageFacts[0].Windows = []UsageWindow{window(b, "session", 300, 2000, 16200)}
	b.Snapshot.UsageFacts[1].Windows = []UsageWindow{window(b, "session", 300, 2000, 1800)}
	if equivalence == DeclaredGroups {
		b.Policy.Headroom.Groups = [][]EquivalentPair{{{Model: "model-a", Effort: "high"}}}
	}
	if equivalence == FitnessBand {
		b.Evaluation.EstimatorPartition = &EstimatorPartition{EstimatorPartitionSchemaVersion, FitnessEstimatorVersion, []EstimatorPartitionGroup{{"ab", []string{"a", "b"}}, {"c", []string{"c"}}, {"local", []string{"local-x"}}}}
	}
	if strategy == StrategyCostWithQualityFloor {
		b.Policy.Cost.BillingOrder = []BillingClass{BillingSubscription, BillingLocal, BillingMetered}
		// Primary quota costs fix subscription base order a, b, c. The local
		// class is last; the fixed-slot vector itself uses quality-first.
		for i := 0; i < 3; i++ {
			b.Evaluation.Costs.Candidates[i].QuotaDemandBP = ptr64(int64(i + 1))
		}
	}
	return b
}
func TestQualityHeadroomGroups(t *testing.T) {
	for _, eq := range []Equivalence{SamePairAnyHome, DeclaredGroups, FitnessBand} {
		t.Run(string(eq), func(t *testing.T) {
			t.Run("fixed_slots_base_positions", func(t *testing.T) {
				b := wrapperBundle(eq, StrategyQualityFirst)
				r := selectBundle(t, b)
				if !slices.Equal(ids(r), []string{"b", "local-x", "a", "c"}) {
					t.Fatalf("order=%v", ids(r))
				}
				for i, pos := range []int64{2, 1, 0, 3} {
					if r.Order[i].BasePosition == nil || *r.Order[i].BasePosition != pos {
						t.Fatalf("base positions=%+v", r.Order)
					}
				}
				routed, err := Route(b)
				if err != nil {
					t.Fatal(err)
				}
				ex, err := ExplainDecision(b, *routed.Decision)
				if err != nil {
					t.Fatal(err)
				}
				for _, x := range ex.Candidates {
					if x.FinalPosition == nil || x.Quality == nil {
						t.Fatal("lost final position or floor explanation")
					}
				}
			})
			for _, tc := range []struct {
				name     string
				strategy StrategyName
				mutate   func(*DecisionBundle)
				code     string
			}{
				{"floor_guard", StrategyQualityFirst, func(b *DecisionBundle) { b.Evaluation.Estimates[1].Fitness = ptr64(5999) }, HeadroomQualityFloorConflict},
				{"cost_knownness_guard", StrategyCostWithQualityFloor, func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[1].LatencyMS = nil }, HeadroomCostKnownnessConflict},
			} {
				t.Run(tc.name, func(t *testing.T) {
					b := wrapperBundle(eq, tc.strategy)
					tc.mutate(&b)
					_, err := Route(b)
					assertCode(t, err, tc.code)
					_, err = Explain(b)
					assertCode(t, err, tc.code)
				})
			}
			t.Run("local_and_metered_excluded_from_guards", func(t *testing.T) {
				b := wrapperBundle(eq, StrategyQualityFirst)
				setBilling(&b, 1, BillingMetered)
				b.Evaluation.Estimates[1].Fitness = ptr64(5999)
				b.Evaluation.Estimates[3].Coverage = nil
				r := selectBundle(t, b)
				if !slices.Equal(ids(r), []string{"a", "c", "b", "local-x"}) {
					t.Fatalf("fixed alternatives moved: %+v", r)
				}
			})
			t.Run("cost_tuple_may_be_overridden_inside_equivalence", func(t *testing.T) {
				b := wrapperBundle(eq, StrategyCostWithQualityFloor)
				r := selectBundle(t, b)
				if !slices.Equal(ids(r), []string{"b", "a", "c", "local-x"}) {
					t.Fatalf("order=%v", ids(r))
				}
			})
		})
	}
}
func TestQualityHeadroomUnknownSingletonsAndOutOfBand(t *testing.T) {
	b := wrapperBundle(FitnessBand, StrategyQualityFirst)
	b.Evaluation.Estimates[1].Fitness = nil
	b.Evaluation.EstimatorPartition.Groups = []EstimatorPartitionGroup{{"known-a", []string{"a"}}, {"known-c", []string{"c"}}, {"local", []string{"local-x"}}, {"singleton:b", []string{"b"}}}
	r := selectBundle(t, b)
	if !slices.Equal(ids(r), []string{"a", "local-x", "c", "b"}) {
		t.Fatalf("singleton/out-of-band moved: %v", ids(r))
	}
	b = wrapperBundle(DeclaredGroups, StrategyQualityFirst)
	b.Policy.Headroom.Groups = [][]EquivalentPair{{{Model: "model-c", Effort: "high"}}}
	r = selectBundle(t, b)
	if !slices.Equal(ids(r), []string{"a", "local-x", "b", "c"}) {
		t.Fatalf("ungrouped slots moved: %v", ids(r))
	}
}
func TestQualityBaseAbstentionBeforePartition(t *testing.T) {
	for _, strategy := range []StrategyName{StrategyQualityFirst, StrategyCostWithQualityFloor} {
		t.Run(string(strategy), func(t *testing.T) {
			b := qualityBundle(2, strategy)
			b.Policy.Headroom.Enabled = true
			b.Policy.Headroom.Equivalence = FitnessBand
			for i := range b.Evaluation.Estimates {
				b.Evaluation.Estimates[i].Fitness = nil
			}
			routed, err := Route(b)
			if err != nil || routed.Decision.Outcome != OutcomeAbstain || !slices.Contains(routed.Decision.ReasonCodes, ReasonQualityFloorUnmet) {
				t.Fatalf("route=%+v err=%v", routed, err)
			}
			ex, err := ExplainDecision(b, *routed.Decision)
			if err != nil || ex.Abstention == nil {
				t.Fatalf("ex=%+v err=%v", ex, err)
			}
			for _, x := range ex.Candidates {
				if x.FinalPosition != nil {
					t.Fatal("abstention fabricated an order")
				}
			}
		})
	}
	b := qualityBundle(1, StrategyCostWithQualityFloor)
	b.Policy.Headroom.Enabled = true
	b.Policy.Headroom.Equivalence = FitnessBand
	b.Evaluation.Costs.Candidates[0].LatencyMS = nil
	routed, err := Route(b)
	if err != nil || !slices.Contains(routed.Decision.ReasonCodes, ReasonCostUnknown) {
		t.Fatalf("cost abstention=%+v %v", routed, err)
	}
	old := fixtureBundle(1)
	old.Policy.Headroom.Equivalence = FitnessBand
	_, err = (HeadroomStrategy{Base: fixedStrategy{result: SelectionResult{Abstention: &Abstention{ReasonQualityFloorUnmet}}}}).Select(old.Snapshot, old.Evaluation, old.Policy, old.Envelope)
	requireErrorCode(t, err, HeadroomPartitionRequired)
}
func TestQualityFloorFallbackCannotEscape(t *testing.T) {
	for _, why := range []string{"floor", "cost", "incomparable", "reserve"} {
		t.Run(why, func(t *testing.T) {
			b := qualityBundle(2, StrategyCostWithQualityFloor)
			switch why {
			case "floor":
				for i := range b.Evaluation.Estimates {
					b.Evaluation.Estimates[i].Fitness = nil
				}
			case "cost":
				for i := range b.Evaluation.Costs.Candidates {
					b.Evaluation.Costs.Candidates[i].LatencyMS = nil
				}
			case "incomparable":
				b.Evaluation.Costs.Candidates[1].QuotaUnit = ptrString("tokens@1")
			case "reserve":
				b.Policy.Headroom.Enabled = true
				b.Policy.Headroom.OnAllReserved = ReservedAbstain
				for i := range b.Snapshot.UsageFacts {
					b.Snapshot.UsageFacts[i].Windows = []UsageWindow{window(b, "session", 300, 9000, 1000)}
				}
			}
			b.Versions.MandatoryEvidenceFloor = false
			routed, err := Route(b)
			if err != nil || routed.Decision.Outcome != OutcomeAbstain {
				t.Fatalf("abstention=%+v err=%v", routed, err)
			}
			allowed := slices.Clone(routed.Decision.ReasonCodes)
			_, err = BaselineFallback(*routed.Decision, b.Snapshot, b.Snapshot.Candidates[0], allowed, false)
			requireErrorCode(t, err, "routing_fallback_not_allowed")
			b.Versions.BaselineFallbackReasons = allowed
			_, err = Route(b)
			requireErrorCode(t, err, "routing_fallback_not_allowed")
		})
	}
}
func TestQualityShadowAndEmptySet(t *testing.T) {
	b := qualityBundle(2, StrategyQualityFirst)
	b.Policy.Mode = ModeShadow
	b.Snapshot.ConfigOrder = []string{"b", "a"}
	routed, err := Route(b)
	if err != nil || routed.Decision.SelectedCandidate.ID != "a" || routed.EffectiveCandidate.ID != "b" {
		t.Fatalf("shadow=%+v %v", routed, err)
	}
	for _, strategy := range []StrategyName{StrategyQualityFirst, StrategyCostWithQualityFloor} {
		t.Run(string(strategy), func(t *testing.T) {
			b := qualityBundle(0, strategy)
			b.Policy.Headroom.Enabled = true
			b.Policy.Headroom.Equivalence = FitnessBand
			// Empty admission bypasses estimator/rubric bindings and partition requirement.
			b.Evaluation.Rubric = nil
			b.Evaluation.EvaluatorVersions = []Provenance{}
			b.Versions.EstimatorVersion = Unknown
			b.Evaluation.Costs = nil
			routed, err := Route(b)
			if err != nil || routed.Decision.Outcome != OutcomeNoEligibleCandidates || routed.EffectiveCandidate != nil {
				t.Fatalf("empty=%+v %v", routed, err)
			}
			ex, err := ExplainDecision(b, *routed.Decision)
			if err != nil || len(ex.Candidates) != 0 {
				t.Fatalf("empty explain=%+v %v", ex, err)
			}
		})
	}
}
func TestQualityWholeSetAllReserved(t *testing.T) {
	for _, strategy := range []StrategyName{StrategyQualityFirst, StrategyCostWithQualityFloor} {
		t.Run(string(strategy), func(t *testing.T) {
			b := qualityBundle(2, strategy)
			b.Policy.Headroom.Enabled = true
			b.Policy.Headroom.Equivalence = DeclaredGroups
			b.Policy.Headroom.OnAllReserved = ReservedAbstain
			b.Evaluation.Estimates[1].Fitness = ptr64(5999)
			b.Snapshot.UsageFacts[0].Windows = []UsageWindow{window(b, "session", 300, 9000, 1000)}
			// Below-floor b is still part of all-reserved quantification.
			r := selectBundle(t, b)
			if r.Abstention != nil {
				t.Fatal("all-reserved checked passing set only")
			}
		})
	}
}
func TestQualityStructuredCostExplanation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		class    BillingClass
		mutate   func(*CandidateCost)
		complete bool
		unit     string
	}{
		{"metered_explicit_zero", BillingMetered, func(c *CandidateCost) { c.APIUSDMicros = ptr64(0) }, true, "usd-micros"},
		{"subscription_exact_unit", BillingSubscription, func(c *CandidateCost) { c.QuotaDemandBP = ptr64(25) }, true, "requests@1"},
		{"local_three_dimensions_zero_start", BillingLocal, func(*CandidateCost) {}, true, "milliseconds"},
		{"metered_latency_only", BillingMetered, func(c *CandidateCost) { c.APIUSDMicros = nil }, false, ""},
		{"unknown_cost_row", BillingMetered, func(c *CandidateCost) { c.APIUSDMicros = nil; c.LatencyMS = nil }, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := qualityBundle(1, StrategyQualityFirst)
			b.Evaluation.Costs = &CostSnapshot{CostsSchemaVersion, "forecast@1", []CandidateCost{knownCost("a", tc.class)}}
			setBilling(&b, 0, tc.class)
			tc.mutate(&b.Evaluation.Costs.Candidates[0])
			ex, err := Explain(b)
			if err != nil {
				t.Fatal(err)
			}
			cost := ex.Candidates[0].Cost
			if cost == nil || !reflect.DeepEqual(cost.Estimate, b.Evaluation.Costs.Candidates[0]) || cost.Complete != tc.complete {
				t.Fatalf("cost=%+v", cost)
			}
			if tc.complete {
				if cost.ComparisonUnit == nil || *cost.ComparisonUnit != tc.unit {
					t.Fatalf("unit=%v", cost.ComparisonUnit)
				}
			} else if cost.ComparisonUnit != nil {
				t.Fatal("incomplete cost has unit")
			}
			raw, _ := json.Marshal(cost)
			if bytes.Contains(raw, []byte("null")) {
				t.Fatalf("null in explain: %s", raw)
			}
			if !tc.complete && bytes.Contains(raw, []byte("comparison_unit")) {
				t.Fatal("unknown unit synthesized")
			}
			if !strings.Contains(ex.RenderHuman(), "cost=") {
				t.Fatal("human explanation omitted structured cost")
			}
			// Explanation owns copies, including explicit-zero pointers.
			if cost.Estimate.LatencyMS != nil {
				*cost.Estimate.LatencyMS = 999
				if *b.Evaluation.Costs.Candidates[0].LatencyMS == 999 {
					t.Fatal("mutable alias in explanation")
				}
			}
		})
	}
	t.Run("evaluation_without_costs", func(t *testing.T) {
		b := qualityBundle(1, StrategyQualityFirst)
		ex, err := Explain(b)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(ex)
		if bytes.Contains(raw, []byte(`"cost"`)) {
			t.Fatal("cost member synthesized")
		}
	})
	t.Run("unused_config_baseline_forecast", func(t *testing.T) {
		b := qualityBundle(1, StrategyCostWithQualityFloor)
		b.Policy.Strategy = StrategyConfigOrder
		b.Policy.Quality = nil
		b.Policy.Cost = nil
		ex, err := Explain(b)
		if err != nil || ex.Candidates[0].Cost == nil || ex.Candidates[0].Quality != nil {
			t.Fatalf("baseline explain=%+v %v", ex, err)
		}
	})
}
func TestQualityVersionDispatchAndPolicyCompatibility(t *testing.T) {
	for _, version := range []string{SelectorVersion, QualitySelectorVersion, "unsupported"} {
		t.Run(version, func(t *testing.T) {
			b := qualityBundle(1, StrategyQualityFirst)
			b.Versions.SelectorVersion = version
			if version == QualitySelectorVersion {
				_, err := Route(b)
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			_, err := Route(b)
			requireErrorCode(t, err, "routing_selector_version_mismatch")
			_, err = Explain(b)
			requireErrorCode(t, err, "routing_selector_version_mismatch")
			_, err = BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, SelectionResult{Abstention: &Abstention{ReasonQualityFloorUnmet}}, b.Versions)
			requireErrorCode(t, err, "routing_selector_version_mismatch")
		})
	}
	for _, strategy := range []StrategyName{StrategyConfigOrder, StrategyQualityFirst} {
		t.Run(string(strategy)+"_rejects_cost_policy", func(t *testing.T) {
			b := qualityBundle(1, StrategyCostWithQualityFloor)
			b.Policy.Strategy = strategy
			_, err := Route(b)
			requireErrorCode(t, err, "routing_invalid_policy")
		})
	}
	b := qualityBundle(1, StrategyQualityFirst)
	b.Evaluation.Estimates[0].Fitness = nil
	_, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, SelectionResult{Order: []RankedCandidate{{CandidateID: "a", Position: 0, ReasonCodes: []ReasonCode{}}}}, b.Versions)
	requireErrorCode(t, err, RoutingSelectionMismatch)
	// Existing floorless policy fixtures continue to validate and hash exactly.
	old := fixture[RoutingPolicy](t, "policy-nondefault")
	digest, err := old.Digest()
	if err != nil || digest != strings.TrimSpace(string(readFixture(t, "policy-nondefault.digest"))) {
		t.Fatalf("legacy digest=%s %v", digest, err)
	}
	requireErrorCode(t, old.ValidateForVersion(QualitySelectorVersion), QualityPolicyRequired)
}

// CQ expected bytes and SHA-256 identities were authored independently with
// literal input/result shapes and Python's JSON/SHA-256, not routing output.
// Tests never rewrite them.
func TestQualityCostCanonicalGoldens(t *testing.T) {
	for _, name := range []string{"quality-first", "cost-with-quality-floor", "headroom-slots"} {
		t.Run(name, func(t *testing.T) {
			read := func(suffix string) []byte {
				t.Helper()
				raw, err := os.ReadFile(filepath.Join("testdata", "cq", name+"."+suffix))
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			b, err := LoadBundle(read("bundle.canonical.json"))
			if err != nil {
				t.Fatal(err)
			}
			routed, err := Route(b)
			if err != nil {
				t.Fatal(err)
			}
			ex, err := Explain(b)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				member string
				value  any
			}{{"bundle", b}, {"policy", b.Policy}, {"evaluation", b.Evaluation}, {"decision", routed.Decision}, {"explain", ex}} {
				got, err := canonical.Marshal(tc.value)
				if err != nil {
					t.Fatal(err)
				}
				want := read(tc.member + ".canonical.json")
				if !bytes.Equal(got, want) {
					t.Fatalf("%s bytes\ngot  %s\nwant %s", tc.member, got, want)
				}
				d, err := canonical.Digest(tc.value)
				if err != nil || d != strings.TrimSpace(string(read(tc.member+".digest"))) {
					t.Fatalf("%s digest=%s %v", tc.member, d, err)
				}
			}
			if routed.Decision.DecisionID != strings.TrimSpace(string(read("decision-id"))) {
				t.Fatal("decision content id differs")
			}
			replay, err := Replay(b, routed.Decision)
			if err != nil || !replay.Equal {
				t.Fatalf("replay=%+v %v", replay, err)
			}
			if name == "quality-first" {
				b.Evaluation.Costs.Candidates[0].APIUSDMicros = ptr64(2)
				changed, err := Replay(b, routed.Decision)
				if err != nil || changed.Equal {
					t.Fatalf("forecast change did not change identity: %+v %v", changed, err)
				}
			}
		})
	}
}

func TestQualityHeadroomExistingFactVectors(t *testing.T) {
	for _, v := range vectors() {
		t.Run(v.name, func(t *testing.T) {
			b := v.bundle
			quality := qualityBundle(len(b.Snapshot.Candidates), StrategyQualityFirst)
			b.Policy.Strategy = quality.Policy.Strategy
			b.Policy.Quality = quality.Policy.Quality
			b.Policy.Quality.MinimumFitnessBP = 0
			b.Policy.Quality.MinimumCoverageBP = 0
			b.Evaluation = quality.Evaluation
			b.Versions.SelectorVersion = QualitySelectorVersion
			b.Versions.EstimatorVersion = FitnessEstimatorVersion
			// Freeze quality scores that reproduce the original base slots, then verify
			// the existing headroom facts still yield exactly the same permutations.
			for i, id := range b.Snapshot.ConfigOrder {
				for j := range b.Evaluation.Estimates {
					if b.Evaluation.Estimates[j].CandidateID == id {
						b.Evaluation.Estimates[j].Fitness = ptr64(10000 - int64(i))
					}
				}
			}
			strategy, err := StrategyForVersion(b.Policy, QualitySelectorVersion)
			var r SelectionResult
			if err == nil {
				r, err = strategy.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			}
			if v.refusal != "" {
				assertCode(t, err, v.refusal)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids(r), v.order) {
				t.Fatalf("order=%v want=%v", ids(r), v.order)
			}
			if err := r.ValidateAgainst(b.Snapshot.Candidates); err != nil {
				t.Fatal(err)
			}
			ex, err := Explain(b)
			if err != nil {
				t.Fatal(err)
			}
			if v.check != nil {
				v.check(t, ex)
			}
			for id, codes := range v.codes {
				for _, x := range ex.Candidates {
					if x.CandidateID == id {
						for _, code := range codes {
							if !slices.Contains(x.ReasonCodes, code) {
								t.Fatalf("%s missing %s", id, code)
							}
						}
					}
				}
			}
		})
	}
}

func TestQualityCostStrictBundleAndSemanticBillingOrder(t *testing.T) {
	b := qualityBundle(2, StrategyCostWithQualityFloor)
	raw, _ := json.Marshal(b)
	for _, tc := range []struct{ name, old, new, code string }{
		{"null_cost_quantity", `"latency_ms":12`, `"latency_ms":null`, canonical.Null},
		{"fractional_cost_quantity", `"latency_ms":12`, `"latency_ms":0.5`, "routing_invalid_input"},
		{"unknown_cost_key", `"quota_demand_bp":10`, `"quota_demand_bp":10,"extra":0`, ContractUnknownField},
		{"wrong_case_cost_key", `"quota_unit":"requests@1"`, `"Quota_unit":"requests@1"`, ContractUnknownField},
		{"missing_floor_threshold_in_bundle", `"minimum_fitness_bp":6000,`, "", ContractMissingField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(string(raw), tc.old, tc.new, 1)
			if changed == string(raw) {
				t.Fatal("replacement absent")
			}
			_, err := LoadBundle([]byte(changed))
			assertCode(t, err, tc.code)
		})
	}
	b.Policy.Cost.BillingOrder = []BillingClass{BillingSubscription, BillingMetered, BillingLocal}
	raw, _ = json.Marshal(b.Policy)
	loaded, err := LoadPolicy(raw)
	if err != nil || !slices.Equal(loaded.Cost.BillingOrder, b.Policy.Cost.BillingOrder) {
		t.Fatalf("semantic order changed: %+v %v", loaded.Cost, err)
	}
	d1, err := loaded.Digest()
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(loaded.Cost.BillingOrder)
	d2, err := loaded.Digest()
	if err != nil || d1 == d2 {
		t.Fatalf("semantic priority not hashed: %s %s %v", d1, d2, err)
	}
	b.Evaluation.Costs.Candidates[0].QuotaDemandBP = ptr64(1000000000000000)
	if err := b.Evaluation.Costs.Validate(); err != nil {
		t.Fatalf("inclusive quantity limit: %v", err)
	}
}

func TestQualityPublicHeadroomWrapperGuardsFloor(t *testing.T) {
	for _, eq := range []Equivalence{SamePairAnyHome, DeclaredGroups, FitnessBand} {
		t.Run(string(eq), func(t *testing.T) {
			b := wrapperBundle(eq, StrategyQualityFirst)
			b.Evaluation.Estimates[1].Fitness = nil
			_, err := (HeadroomStrategy{Base: QualityFirstStrategy{}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			requireErrorCode(t, err, HeadroomQualityFloorConflict)
		})
	}
}

func TestQualityPublicHeadroomWrapperEmptyAndAbstention(t *testing.T) {
	for _, eq := range []Equivalence{SamePairAnyHome, DeclaredGroups, FitnessBand} {
		t.Run(string(eq), func(t *testing.T) {
			b := qualityBundle(2, StrategyCostWithQualityFloor)
			b.Policy.Headroom.Enabled = true
			b.Policy.Headroom.Equivalence = eq
			if eq == DeclaredGroups {
				b.Policy.Headroom.Groups = [][]EquivalentPair{{{Model: "model-a", Effort: "high"}}}
			}
			// Valid input plus base abstention precedes the absent-partition requirement.
			want := SelectionResult{Abstention: &Abstention{ReasonQualityFloorUnmet}}
			got, err := (HeadroomStrategy{Base: fixedStrategy{result: want}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("base abstention changed: got=%+v err=%v", got, err)
			}
			b = qualityBundle(0, StrategyCostWithQualityFloor)
			b.Policy.Headroom.Enabled = true
			b.Policy.Headroom.Equivalence = eq
			b.Evaluation.Rubric = nil
			b.Evaluation.Costs = nil
			got, err = (HeadroomStrategy{Base: ConfigOrderStrategy{}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			if err != nil || len(got.Order) != 0 || got.Abstention != nil {
				t.Fatalf("empty base order changed: got=%+v err=%v", got, err)
			}
		})
	}
}
