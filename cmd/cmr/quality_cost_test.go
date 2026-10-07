package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

func TestQualityCostCLIStoredVectors(t *testing.T) {
	for _, name := range []string{"quality-first", "cost-with-quality-floor", "headroom-slots"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join("..", "..", "pkg", "routing", "testdata", "cq")
			bundle := filepath.Join(root, name+".bundle.canonical.json")
			decision := filepath.Join(root, name+".decision.canonical.json")
			for _, cmd := range []string{"route", "explain", "replay"} {
				t.Run(cmd, func(t *testing.T) {
					args := []string{cmd, "--input", bundle, "--json"}
					if cmd != "route" {
						args = append(args, "--decision", decision)
					}
					var out, stderr bytes.Buffer
					if code := run(args, &out, &stderr); code != 0 || stderr.Len() != 0 {
						t.Fatalf("code=%d out=%s err=%s", code, &out, &stderr)
					}
					switch cmd {
					case "route":
						var r routing.RouteResult
						if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Decision == nil || r.Decision.SelectorVersion != routing.QualitySelectorVersion || r.EffectiveCandidate.ID != "b" {
							t.Fatalf("route=%s %v", &out, err)
						}
					case "explain":
						var ex routing.Explanation
						if err := json.Unmarshal(out.Bytes(), &ex); err != nil || ex.SelectedCandidateID != "b" || len(ex.Candidates) == 0 || ex.Candidates[0].Quality == nil {
							t.Fatalf("explain=%s %v", &out, err)
						}
						if name != "headroom-slots" && ex.Candidates[0].Cost == nil {
							t.Fatal("lost bound forecast")
						}
					case "replay":
						if !strings.Contains(out.String(), `"equal":true`) {
							t.Fatalf("replay=%s", &out)
						}
					}
				})
			}
		})
	}
}

func qualityCLIPtr64(n int64) *int64       { return &n }
func qualityCLIPtrString(s string) *string { return &s }
func qualityCLIKnownCost(id string, class routing.BillingClass) routing.CandidateCost {
	row := routing.CandidateCost{CandidateID: id, LatencyMS: qualityCLIPtr64(12), Source: routing.Provenance{Name: "forecast", Version: "1", Digest: "sha256:" + strings.Repeat("a", 64)}}
	if class == routing.BillingSubscription {
		row.QuotaDemandBP, row.QuotaUnit = qualityCLIPtr64(10), qualityCLIPtrString("requests@1")
	} else {
		row.EngineOccupancyMS, row.StartMS = qualityCLIPtr64(30), qualityCLIPtr64(0)
	}
	return row
}

// Each invalid API class also reaches all three CLI wire entry paths. The
// original valid decision stays fixed while only its supplied bundle is corrupted.
func TestQualityCostCLIInvalidInputMatrix(t *testing.T) {
	root := filepath.Join("..", "..", "pkg", "routing", "testdata", "cq")
	raw, err := os.ReadFile(filepath.Join(root, "headroom-slots.bundle.canonical.json"))
	if err != nil {
		t.Fatal(err)
	}
	valid, err := routing.LoadBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	valid.Policy.Strategy = routing.StrategyCostWithQualityFloor
	valid.Policy.Headroom.Equivalence = routing.SamePairAnyHome
	valid.Policy.Cost = &routing.CostPolicy{Version: routing.CostComparatorVersion, ModelVersion: "forecast@1", BillingOrder: []routing.BillingClass{routing.BillingSubscription, routing.BillingLocal, routing.BillingMetered}}
	valid.Evaluation.EstimatorPartition = nil
	valid.Evaluation.Costs = &routing.CostSnapshot{SchemaVersion: routing.CostsSchemaVersion, ModelVersion: "forecast@1", Candidates: []routing.CandidateCost{}}
	for _, c := range valid.Snapshot.Candidates {
		valid.Evaluation.Costs.Candidates = append(valid.Evaluation.Costs.Candidates, qualityCLIKnownCost(c.ID, c.BillingClass))
	}
	routed, err := routing.Route(valid)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input, decision := filepath.Join(dir, "input.json"), filepath.Join(dir, "decision.json")
	write := func(path string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	decisionRaw, err := json.Marshal(routed.Decision)
	if err != nil {
		t.Fatal(err)
	}
	write(decision, decisionRaw)
	validRaw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, raw []byte, want string) {
		t.Helper()
		write(input, raw)
		for _, cmd := range []string{"route", "explain", "replay"} {
			for _, asJSON := range []bool{false, true} {
				name := cmd + "/human"
				if asJSON {
					name = cmd + "/json"
				}
				t.Run(name, func(t *testing.T) {
					args := []string{cmd, "--input", input}
					if cmd != "route" {
						args = append(args, "--decision", decision)
					}
					if asJSON {
						args = append(args, "--json")
					}
					var out, stderr bytes.Buffer
					exit := run(args, &out, &stderr)
					if want == "" {
						if exit != 0 || stderr.Len() != 0 {
							t.Fatalf("valid exit=%d out=%s stderr=%s", exit, &out, &stderr)
						}
						return
					}
					if exit != 2 {
						t.Fatalf("exit=%d out=%s stderr=%s; want typed refusal %s", exit, &out, &stderr, want)
					}
					if asJSON {
						var refusal struct {
							Error Refusal `json:"error"`
						}
						if err := json.Unmarshal(out.Bytes(), &refusal); err != nil || refusal.Error.Code != want || stderr.Len() != 0 {
							t.Fatalf("refusal=%s stderr=%s err=%v; want %s", &out, &stderr, err, want)
						}
					} else if out.Len() != 0 || !strings.Contains(stderr.String(), want+":") {
						t.Fatalf("out=%s stderr=%s; want %s", &out, &stderr, want)
					}
				})
			}
		}
	}
	t.Run("valid", func(t *testing.T) { check(t, validRaw, "") })
	cases := []struct {
		name   string
		mutate func(*routing.DecisionBundle)
		code   string
	}{
		{"invalid_partition", func(b *routing.DecisionBundle) {
			b.Evaluation.EstimatorPartition = &routing.EstimatorPartition{SchemaVersion: "unsupported", EstimatorVersion: routing.FitnessEstimatorVersion, Groups: []routing.EstimatorPartitionGroup{}}
		}, routing.InvalidEstimatorPartition},
		{"partition_membership", func(b *routing.DecisionBundle) {
			b.Evaluation.EstimatorPartition = &routing.EstimatorPartition{SchemaVersion: routing.EstimatorPartitionSchemaVersion, EstimatorVersion: routing.FitnessEstimatorVersion, Groups: []routing.EstimatorPartitionGroup{{ID: "only", CandidateIDs: []string{"a"}}}}
		}, routing.InvalidEstimatorPartition},
		{"partition_version", func(b *routing.DecisionBundle) {
			b.Evaluation.EstimatorPartition = &routing.EstimatorPartition{SchemaVersion: routing.EstimatorPartitionSchemaVersion, EstimatorVersion: "other", Groups: []routing.EstimatorPartitionGroup{{ID: "all", CandidateIDs: []string{"a", "b", "c", "local-x"}}}}
		}, routing.EstimatorVersionMismatch},
		{"estimate_set_before_partition", func(b *routing.DecisionBundle) {
			b.Evaluation.Estimates = b.Evaluation.Estimates[:1]
			b.Evaluation.EstimatorPartition = &routing.EstimatorPartition{SchemaVersion: "unsupported", EstimatorVersion: routing.FitnessEstimatorVersion, Groups: []routing.EstimatorPartitionGroup{}}
		}, routing.EstimateSetMismatch},
		{"substituted_cost_row", func(b *routing.DecisionBundle) {
			b.Evaluation.Costs.Candidates[len(b.Evaluation.Costs.Candidates)-1].CandidateID = "z"
		}, routing.InvalidCost},
		{"quality_required", func(b *routing.DecisionBundle) { b.Policy.Quality = nil }, routing.QualityPolicyRequired},
		{"cost_policy_required", func(b *routing.DecisionBundle) { b.Policy.Cost = nil }, routing.CostPolicyRequired},
		{"cost_snapshot_required", func(b *routing.DecisionBundle) { b.Evaluation.Costs = nil }, routing.CostPolicyRequired},
		{"unsupported_scale", func(b *routing.DecisionBundle) { b.Policy.Quality.Scale = "probability" }, routing.QualityVersionMismatch},
		{"unknown_floor_rubric", func(b *routing.DecisionBundle) { b.Policy.Quality.RubricVersion = routing.Unknown }, routing.QualityVersionMismatch},
		{"rubric_mismatch", func(b *routing.DecisionBundle) { b.Evaluation.Rubric.ID = "other" }, routing.QualityVersionMismatch},
		{"assessment_projection_mismatch", func(b *routing.DecisionBundle) {
			b.Evaluation.Assessments[0].Requirements = routing.Requirements{Items: []routing.Requirement{{Category: "review.spec"}}}
		}, routing.QualityVersionMismatch},
		{"estimate_version_mismatch", func(b *routing.DecisionBundle) { b.Evaluation.Estimates[0].EstimatorVersion = "other" }, routing.QualityVersionMismatch},
		{"provenance_version_mismatch", func(b *routing.DecisionBundle) { b.Evaluation.EvaluatorVersions[0].Version = "other" }, routing.QualityVersionMismatch},
		{"missing_estimator_provenance", func(b *routing.DecisionBundle) { b.Evaluation.EvaluatorVersions = []routing.Provenance{} }, routing.QualityVersionMismatch},
		{"empty_bundle_version", func(b *routing.DecisionBundle) { b.Versions.EstimatorVersion = "" }, routing.ContractEmptyField},
		{"bundle_version_mismatch", func(b *routing.DecisionBundle) { b.Versions.EstimatorVersion = "other" }, routing.QualityVersionMismatch},
		{"missing_estimate", func(b *routing.DecisionBundle) { b.Evaluation.Estimates = b.Evaluation.Estimates[:1] }, routing.EstimateSetMismatch},
		{"extra_estimate", func(b *routing.DecisionBundle) {
			b.Evaluation.Estimates = append(b.Evaluation.Estimates, routing.Estimate{CandidateID: "z", ContributingRecordIDs: []string{}, EstimatorVersion: routing.FitnessEstimatorVersion})
		}, routing.EstimateSetMismatch},
		{"substituted_estimate", func(b *routing.DecisionBundle) {
			b.Evaluation.Estimates[len(b.Evaluation.Estimates)-1].CandidateID = "z"
		}, routing.EstimateSetMismatch},
		{"duplicate_estimate", func(b *routing.DecisionBundle) { b.Evaluation.Estimates[1].CandidateID = "a" }, canonical.DuplicateKey},
		{"invalid_estimate", func(b *routing.DecisionBundle) { b.Evaluation.Estimates[0].Fitness = qualityCLIPtr64(10001) }, "routing_invalid_estimate"},
		{"missing_cost_row", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates = b.Evaluation.Costs.Candidates[:1] }, routing.InvalidCost},
		{"extra_cost_row", func(b *routing.DecisionBundle) {
			b.Evaluation.Costs.Candidates = append(b.Evaluation.Costs.Candidates, qualityCLIKnownCost("z", routing.BillingSubscription))
		}, routing.InvalidCost},
		{"duplicate_cost_row", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[1].CandidateID = "a" }, canonical.DuplicateKey},
		{"unordered_cost_rows", func(b *routing.DecisionBundle) { slices.Reverse(b.Evaluation.Costs.Candidates) }, canonical.Unordered},
		{"negative_cost", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[0].LatencyMS = qualityCLIPtr64(-1) }, routing.InvalidCost},
		{"too_large_cost", func(b *routing.DecisionBundle) {
			b.Evaluation.Costs.Candidates[0].QuotaDemandBP = qualityCLIPtr64(1000000000000001)
		}, routing.InvalidCost},
		{"class_inapplicable_cost", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[0].APIUSDMicros = qualityCLIPtr64(1) }, routing.InvalidCost},
		{"quota_without_unit", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[0].QuotaUnit = nil }, routing.InvalidCost},
		{"unit_without_demand", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[0].QuotaDemandBP = nil }, routing.InvalidCost},
		{"unknown_unit", func(b *routing.DecisionBundle) {
			b.Evaluation.Costs.Candidates[0].QuotaUnit = qualityCLIPtrString(routing.Unknown)
		}, routing.InvalidCost},
		{"source_version_absent", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[0].Source.Version = "" }, routing.InvalidCost},
		{"source_digest_absent", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[0].Source.Digest = "" }, routing.InvalidCost},
		{"bad_source_digest", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[0].Source.Digest = "bad" }, routing.ContractInvalidDigest},
		{"cost_model_mismatch", func(b *routing.DecisionBundle) { b.Evaluation.Costs.ModelVersion = "other" }, routing.CostVersionMismatch},
		{"cost_model_unknown", func(b *routing.DecisionBundle) { b.Policy.Cost.ModelVersion = routing.Unknown }, routing.CostVersionMismatch},
		{"cost_schema_unsupported", func(b *routing.DecisionBundle) { b.Evaluation.Costs.SchemaVersion = "other" }, routing.CostVersionMismatch},
		{"cost_comparator_unsupported", func(b *routing.DecisionBundle) { b.Policy.Cost.Version = "other" }, routing.CostVersionMismatch},
		{"malformed_billing_order", func(b *routing.DecisionBundle) {
			b.Policy.Cost.BillingOrder = []routing.BillingClass{routing.BillingLocal}
		}, "routing_invalid_policy"},
		{"duplicate_billing_order", func(b *routing.DecisionBundle) {
			b.Policy.Cost.BillingOrder = []routing.BillingClass{routing.BillingLocal, routing.BillingLocal, routing.BillingMetered}
		}, "routing_invalid_policy"},
		{"billing_order_unknown_class", func(b *routing.DecisionBundle) {
			b.Policy.Cost.BillingOrder = []routing.BillingClass{routing.BillingLocal, routing.BillingSubscription, "other"}
		}, "routing_unknown_enum"},
		{"floor_out_of_range", func(b *routing.DecisionBundle) { b.Policy.Quality.MinimumFitnessBP = 10001 }, "routing_invalid_policy"},
		{"headroom_floor_conflict", func(b *routing.DecisionBundle) { b.Evaluation.Estimates[0].Fitness = qualityCLIPtr64(5999) }, routing.HeadroomQualityFloorConflict},
		{"headroom_knownness_conflict", func(b *routing.DecisionBundle) { b.Evaluation.Costs.Candidates[0].LatencyMS = nil }, routing.HeadroomCostKnownnessConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bad routing.DecisionBundle
			if err := json.Unmarshal(validRaw, &bad); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&bad)
			raw, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			check(t, raw, tc.code)
		})
	}
	for _, tc := range []struct{ name, old, new, code string }{
		{"missing_fitness_threshold", `"minimum_fitness_bp":6000,`, "", routing.ContractMissingField},
		{"missing_coverage_threshold", `,"minimum_coverage_bp":5000`, "", routing.ContractMissingField},
		{"fractional_floor", `"minimum_fitness_bp":6000`, `"minimum_fitness_bp":0.9`, "routing_invalid_policy"},
		{"null_floor", `"minimum_fitness_bp":6000`, `"minimum_fitness_bp":null`, canonical.Null},
		{"wrong_case_floor", `"minimum_fitness_bp":6000`, `"Minimum_fitness_bp":6000`, routing.ContractUnknownField},
		{"unknown_quality_key", `"scale":"role-utility-bp-v1"`, `"scale":"role-utility-bp-v1","extra":0`, routing.ContractUnknownField},
		{"null_cost_quantity", `"latency_ms":12`, `"latency_ms":null`, canonical.Null},
		{"fractional_cost_quantity", `"latency_ms":12`, `"latency_ms":0.5`, "routing_invalid_input"},
		{"unknown_cost_key", `"quota_demand_bp":10`, `"quota_demand_bp":10,"extra":0`, routing.ContractUnknownField},
		{"wrong_case_cost_key", `"quota_unit":"requests@1"`, `"Quota_unit":"requests@1"`, routing.ContractUnknownField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := bytes.Replace(validRaw, []byte(tc.old), []byte(tc.new), 1)
			if bytes.Equal(changed, validRaw) {
				t.Fatal("wire replacement absent")
			}
			check(t, changed, tc.code)
		})
	}
}
