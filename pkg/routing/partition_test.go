package routing

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func c2Bundle(n int, groups ...EstimatorPartitionGroup) DecisionBundle {
	b := fixtureBundle(n)
	b.Policy.Headroom.Equivalence = FitnessBand
	b.Versions.EstimatorVersion = FitnessEstimatorVersion
	b.Evaluation.EvaluatorVersions = []Provenance{{Name: "fitness-estimator", Version: FitnessEstimatorVersion}}
	b.Evaluation.EstimatorPartition = &EstimatorPartition{EstimatorPartitionSchemaVersion, FitnessEstimatorVersion, groups}
	for _, c := range b.Snapshot.Candidates {
		b.Evaluation.Estimates = append(b.Evaluation.Estimates, Estimate{CandidateID: c.ID, Fitness: ptr64(6000), Coverage: ptr64(10000), ContributingRecordIDs: []string{}, EstimatorVersion: FitnessEstimatorVersion})
	}
	return b
}

func TestC2AbsentPartitionOldEvaluation(t *testing.T) {
	e := fixture[EvaluationSnapshot](t, "evaluation")
	raw, err := canonical.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	old, err := canonical.Canonicalize(readFixture(t, "evaluation.json"))
	if err != nil || !bytes.Equal(raw, old) || bytes.Contains(raw, []byte("estimator_partition")) {
		t.Fatalf("absent partition changed old bytes: %s %v", raw, err)
	}
	d, err := e.Digest()
	if err != nil || d != strings.TrimSpace(string(readFixture(t, "evaluation.digest"))) {
		t.Fatalf("digest=%s %v", d, err)
	}
}

func TestC2PartitionValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DecisionBundle)
		code   string
	}{
		{"valid", func(*DecisionBundle) {}, ""},
		{"bad_schema", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.SchemaVersion = "future" }, InvalidEstimatorPartition},
		{"empty_schema", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.SchemaVersion = "" }, ContractEmptyField},
		{"nil_groups", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups = nil }, ContractMissingField},
		{"empty_groups_nonempty_candidates", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups = []EstimatorPartitionGroup{} }, InvalidEstimatorPartition},
		{"nil_members", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups[0].CandidateIDs = nil }, ContractMissingField},
		{"empty_group", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups[0].CandidateIDs = []string{} }, InvalidEstimatorPartition},
		{"empty_group_id", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups[0].ID = "" }, ContractEmptyField},
		{"empty_member_id", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups[0].CandidateIDs[0] = "" }, ContractEmptyField},
		{"overlap", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups[1].CandidateIDs = []string{"a", "b"} }, InvalidEstimatorPartition},
		{"extra_member", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition.Groups[1].CandidateIDs = []string{"b", "extra"}
		}, InvalidEstimatorPartition},
		{"missing_member", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition.Groups = b.Evaluation.EstimatorPartition.Groups[:1]
		}, InvalidEstimatorPartition},
		{"unordered_groups", func(b *DecisionBundle) { slices.Reverse(b.Evaluation.EstimatorPartition.Groups) }, canonical.Unordered},
		{"duplicate_groups", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups[1].ID = "g-a" }, canonical.DuplicateKey},
		{"unordered_members", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition.Groups = []EstimatorPartitionGroup{{"g", []string{"b", "a"}}}
		}, canonical.Unordered},
		{"duplicate_members", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.Groups[0].CandidateIDs = []string{"a", "a"} }, canonical.DuplicateKey},
		{"nonadjacent_duplicate_members", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition.Groups[0].CandidateIDs = []string{"a", "b", "a"}
		}, canonical.DuplicateKey},
		{"missing_estimate", func(b *DecisionBundle) { b.Evaluation.Estimates = b.Evaluation.Estimates[:1] }, InvalidEstimatorPartition},
		{"extra_estimate", func(b *DecisionBundle) {
			e := b.Evaluation.Estimates[1]
			e.CandidateID = "extra"
			b.Evaluation.Estimates = append(b.Evaluation.Estimates, e)
		}, InvalidEstimatorPartition},
		{"substituted_estimate", func(b *DecisionBundle) { b.Evaluation.Estimates[1].CandidateID = "extra" }, InvalidEstimatorPartition},
		{"duplicate_estimates", func(b *DecisionBundle) { b.Evaluation.Estimates[1].CandidateID = "a" }, canonical.DuplicateKey},
		{"unordered_estimates", func(b *DecisionBundle) { slices.Reverse(b.Evaluation.Estimates) }, canonical.Unordered},
		{"mixed_estimator_versions", func(b *DecisionBundle) { b.Evaluation.Estimates[1].EstimatorVersion = "future" }, EstimatorVersionMismatch},
		{"unknown_partition_version", func(b *DecisionBundle) { b.Evaluation.EstimatorPartition.EstimatorVersion = Unknown }, EstimatorVersionMismatch},
		{"unknown_all_versions", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition.EstimatorVersion = "future"
			for i := range b.Evaluation.Estimates {
				b.Evaluation.Estimates[i].EstimatorVersion = "future"
			}
			b.Evaluation.EvaluatorVersions[0].Version = "future"
			b.Versions.EstimatorVersion = "future"
		}, EstimatorVersionMismatch},
		{"missing_provenance", func(b *DecisionBundle) { b.Evaluation.EvaluatorVersions = []Provenance{} }, EstimatorVersionMismatch},
		{"unknown_provenance", func(b *DecisionBundle) { b.Evaluation.EvaluatorVersions[0].Version = Unknown }, EstimatorVersionMismatch},
		{"bundle_version", func(b *DecisionBundle) { b.Versions.EstimatorVersion = Unknown }, EstimatorVersionMismatch},
		{"empty_bundle_version", func(b *DecisionBundle) { b.Versions.EstimatorVersion = "" }, ContractEmptyField},
		{"admitted_set_differs", func(b *DecisionBundle) { b.Snapshot.Candidates[1].ID = "c"; b.Snapshot.ConfigOrder[1] = "c" }, InvalidEstimatorPartition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := c2Bundle(2, EstimatorPartitionGroup{"g-a", []string{"a"}}, EstimatorPartitionGroup{"g-b", []string{"b"}})
			b.Policy.Mode = ModeShadow // mode off evaluates nothing; see TestC2ModeOffIgnoresPartition
			tc.mutate(&b)
			before, _ := json.Marshal(b)
			_, err := Route(b)
			assertCode(t, err, tc.code)
			_, err = Explain(b)
			assertCode(t, err, tc.code)
			_, err = LoadBundle(before)
			wireCode := tc.code
			if tc.name == "nil_groups" || tc.name == "nil_members" {
				wireCode = canonical.Null
			}
			assertCode(t, err, wireCode)
			// Also check direct decision construction, without selector validation.
			_, err = BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, fixedOrder(b, b.Snapshot.ConfigOrder), b.Versions)
			assertCode(t, err, tc.code)
			after, _ := json.Marshal(b)
			if !bytes.Equal(before, after) {
				t.Fatal("validation repaired frozen inputs")
			}
		})
	}
}

func TestC2EmptyCandidateEstimateSet(t *testing.T) {
	b := c2Bundle(0, []EstimatorPartitionGroup{}...)
	_, err := Route(b)
	if err != nil {
		t.Fatal(err)
	}
	p := b.Evaluation.EstimatorPartition
	assertCode(t, p.ValidateAgainst(nil, []Estimate{}), ContractMissingField)
	assertCode(t, p.ValidateAgainst([]ExecutionCandidate{}, nil), ContractMissingField)
}

func TestC2ValidationLayers(t *testing.T) {
	b := c2Bundle(2, EstimatorPartitionGroup{"g", []string{"a", "b"}})
	b.Evaluation.EstimatorPartition.EstimatorVersion = Unknown
	assertCode(t, b.Evaluation.EstimatorPartition.Validate(), "")
	assertCode(t, b.Evaluation.EstimatorPartition.ValidateAgainst(b.Snapshot.Candidates, b.Evaluation.Estimates), EstimatorVersionMismatch)
	b.Evaluation.EstimatorPartition.EstimatorVersion = FitnessEstimatorVersion
	b.Evaluation.EvaluatorVersions = []Provenance{}
	assertCode(t, b.Evaluation.EstimatorPartition.ValidateAgainst(b.Snapshot.Candidates, b.Evaluation.Estimates), "")
	assertCode(t, b.Evaluation.Validate(), EstimatorVersionMismatch)
}

func TestC2PartitionStrictWire(t *testing.T) {
	b := c2Bundle(1, EstimatorPartitionGroup{"g", []string{"a"}})
	raw, _ := json.Marshal(b)
	for _, tc := range []struct{ name, old, replacement, code string }{
		{"unknown_partition_field", `"groups":[`, `"Groups":[`, ContractUnknownField},
		{"unknown_member_field", `"candidate_ids":[`, `"Candidate_ids":[`, ContractUnknownField},
		{"duplicate_json_key", `"candidate_ids":[`, `"id":"duplicate","candidate_ids":[`, canonical.DuplicateKey},
		{"null_members", `"candidate_ids":["a"]`, `"candidate_ids":null`, canonical.Null},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadBundle(bytes.Replace(raw, []byte(tc.old), []byte(tc.replacement), 1))
			assertCode(t, err, tc.code)
		})
	}
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	var evaluation map[string]json.RawMessage
	_ = json.Unmarshal(object["evaluation"], &evaluation)
	evaluation["estimator_partition"] = json.RawMessage(`null`)
	object["evaluation"], _ = json.Marshal(evaluation)
	raw, _ = json.Marshal(object)
	_, err := LoadBundle(raw)
	assertCode(t, err, canonical.Null)
}

// These are frozen producer fixtures, not a routing implementation of Partition.
// Pin the producer's labels/config digests while checking the consuming contract.
func TestC2FrozenBucketVectors(t *testing.T) {
	const config1 = "sha256:60f42d9a9500dad6e4c336153c489b40493aec8fb781d759636a6ffe8861a73a"
	const config250 = "sha256:1fb369fc55aecb2c366e07618df119640deb315a9f60ca329d245a6510dd2b28"
	const f6000 = "fitness-band:a7ce053f279377d9218df7139d0c97d21bffcf916ac3a0c877f418959731bf4c"
	const f6001 = "fitness-band:5fd2b53b9a9eb5d36ae4d1c3f46f03a3d8e504f84be689262ab4f315d0d29713"
	const f10000 = "fitness-band:133ba417497953b192c4af31c408a6706967510dc1c81aa34c311ce3ac9a6331"
	const bin0 = "fitness-band:162e63bc3a5e53ddf260a7bcb4c649e4ea5196d147c8205771fcb028ab88c37c"
	const bin1 = "fitness-band:cbf2811fc300f18f7aa996c7cb637a02bc1e0ce01098108b089109edd1100c0e"
	const bin2 = "fitness-band:bb55d4b20be9c18124b90e6ba9fdd551e97065af4a46b8aaa5b36b7e06330aac"
	for _, tc := range []struct {
		name    string
		width   int64
		config  string
		fitness []*int64
		buckets []int64
		labels  []string
	}{
		{"equal_known_width_1", 1, config1, []*int64{ptr64(6000), ptr64(6000)}, []int64{6000, 6000}, []string{f6000, f6000}},
		{"one_bp_difference_width_1", 1, config1, []*int64{ptr64(6000), ptr64(6001)}, []int64{6000, 6001}, []string{f6000, f6001}},
		{"terminal_fitness_10000", 1, config1, []*int64{ptr64(10000)}, []int64{10000}, []string{f10000}},
		{"unknown_a_and_b_singletons", 1, config1, []*int64{nil, nil}, []int64{-1, -1}, []string{"singleton:a", "singleton:b"}},
		{"width_250_no_chaining", 250, config250, []*int64{ptr64(249), ptr64(250), ptr64(499), ptr64(500)}, []int64{0, 1, 1, 2}, []string{bin0, bin1, bin1, bin2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := struct {
				SchemaVersion string `json:"schema_version"`
				Version       string `json:"version"`
				FitnessWidth  int64  `json:"fitness_band_width_bp"`
				CoverageWidth int64  `json:"coverage_band_width_bp"`
			}{"fitness-config-v1", FitnessEstimatorVersion, tc.width, 1}
			d, err := canonical.Digest(config)
			if err != nil || d != tc.config {
				t.Fatalf("config=%s %v", d, err)
			}
			b := c2Bundle(len(tc.fitness), []EstimatorPartitionGroup{}...)
			members := map[string][]string{}
			for i, f := range tc.fitness {
				b.Evaluation.Estimates[i].Fitness = f
				label := tc.labels[i]
				if f != nil {
					if *f/tc.width != tc.buckets[i] {
						t.Fatal("fixture bucket disagrees with fixed bins")
					}
					key := struct {
						SchemaVersion    string `json:"schema_version"`
						EstimatorVersion string `json:"estimator_version"`
						ConfigDigest     string `json:"config_digest"`
						FitnessBucket    int64  `json:"fitness_bucket"`
						CoverageBucket   int64  `json:"coverage_bucket"`
					}{"fitness-group-v1", FitnessEstimatorVersion, d, tc.buckets[i], 10000}
					gd, err := canonical.Digest(key)
					if err != nil || "fitness-band:"+strings.TrimPrefix(gd, "sha256:") != label {
						t.Fatalf("label=%s digest=%s %v", label, gd, err)
					}
				}
				members[label] = append(members[label], b.Snapshot.Candidates[i].ID)
			}
			for label, ids := range members {
				b.Evaluation.EstimatorPartition.Groups = append(b.Evaluation.EstimatorPartition.Groups, EstimatorPartitionGroup{label, ids})
			}
			slices.SortFunc(b.Evaluation.EstimatorPartition.Groups, func(a, b EstimatorPartitionGroup) int { return strings.Compare(a.ID, b.ID) })
			if err := b.Evaluation.ValidateAgainst(b.Snapshot.Candidates); err != nil {
				t.Fatal(err)
			}
			golden(t, "testdata/c2/"+tc.name+".json", encoded(t, b.Evaluation.EstimatorPartition))
		})
	}
}

func TestC2FitnessBandUsesMembershipAndFixedSlots(t *testing.T) {
	b := c2Bundle(6, EstimatorPartitionGroup{"opaque-band", []string{"a", "b", "d", "e"}}, EstimatorPartitionGroup{"singleton:c", []string{"c"}}, EstimatorPartitionGroup{"singleton:f", []string{"f"}})
	b.Snapshot.Candidates[1].BillingClass = BillingMetered
	b.Snapshot.Candidates[1].UsageKey = nil
	b.Snapshot.Candidates[3].BillingClass = BillingLocal
	b.Snapshot.Candidates[3].UsageKey = nil
	b.Snapshot.Candidates[4].Model = "other-model"
	b.Evaluation.Estimates[4].Fitness = ptr64(1) // Deliberately disagrees with a's raw score.
	b.Snapshot.Inflight[0].Runs = 10
	b.Snapshot.Inflight[2].Runs = 20 // c must keep its singleton slot.
	b.Snapshot.Inflight[5].Runs = 0  // f must not join a/e even with equal fitness.
	b.Snapshot.ConfigOrder = []string{"f", "a", "b", "c", "d", "e"}
	r, ex, err := (HeadroomStrategy{Base: ConfigOrderStrategy{}}).evaluate(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"f", "e", "b", "c", "d", "a"}
	for i, ranked := range r.Order {
		if ranked.CandidateID != want[i] || ranked.Position != int64(i) || *ranked.BasePosition != int64(slices.Index(b.Snapshot.ConfigOrder, ranked.CandidateID)) {
			t.Fatalf("order=%+v", r.Order)
		}
	}
	for _, x := range ex.Candidates {
		if x.Group == "" {
			t.Fatal("partition label missing from explanation")
		}
	}
}

func TestC2SuccessfulFitnessBandPartitionAbsent(t *testing.T) {
	b := fixtureBundle(2)
	b.Policy.Headroom.Equivalence = FitnessBand
	_, err := Route(b)
	assertCode(t, err, HeadroomPartitionRequired)
}

func TestC2SelectionReplayPartitionPresence(t *testing.T) {
	for _, present := range []bool{false, true} {
		name := "absent_partition_stays_omitted"
		b := fixtureBundle(2)
		if present {
			name = "stored_fitness_band_partition_and_orders_equal"
			b = c2Bundle(2, EstimatorPartitionGroup{"opaque", []string{"a", "b"}})
			b.Snapshot.Inflight[0].Runs = 2
		}
		t.Run(name, func(t *testing.T) {
			before, err := canonical.Marshal(b.Evaluation)
			if err != nil {
				t.Fatal(err)
			}
			routed, err := Route(b)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(b)
			loaded, err := LoadBundle(raw)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := Replay(loaded, routed.Decision)
			if err != nil || !replayed.Equal {
				t.Fatalf("replay=%+v %v", replayed, err)
			}
			after, err := canonical.Marshal(loaded.Evaluation)
			if err != nil || !bytes.Equal(before, after) || (loaded.Evaluation.EstimatorPartition != nil) != present {
				t.Fatalf("evaluation changed: %s %v", after, err)
			}
		})
	}
}

func TestC2SelectionReplaySubstitutedPartition(t *testing.T) {
	b := c2Bundle(2, EstimatorPartitionGroup{"g", []string{"a", "b"}})
	b.Snapshot.Inflight[0].Runs = 2
	routed, err := Route(b)
	if err != nil {
		t.Fatal(err)
	}
	before, err := canonical.Marshal(b.Evaluation)
	if err != nil {
		t.Fatal(err)
	}
	b.Evaluation.EstimatorPartition = &EstimatorPartition{EstimatorPartitionSchemaVersion, FitnessEstimatorVersion, []EstimatorPartitionGroup{{"g-a", []string{"a"}}, {"g-b", []string{"b"}}}}
	if err := b.Evaluation.ValidateAgainst(b.Snapshot.Candidates); err != nil {
		t.Fatal(err)
	}
	result, err := Replay(b, routed.Decision)
	if err != nil || result.Equal {
		t.Fatalf("substituted replay=%+v %v", result, err)
	}
	found := false
	for _, diff := range result.Differences {
		found = found || diff.Member == "$.evaluation_snapshot_digest"
	}
	if !found {
		t.Fatalf("partition substitution not bound: %+v", result)
	}
	if routed.Decision.SelectedCandidate.ID != "b" || !bytes.Contains(before, []byte(`"candidate_ids":["a","b"]`)) {
		t.Fatal("stored decision or prior evaluation changed")
	}
}

// Estimation replay is owned by the future evidence implementation. This seam
// test pins comparison of supplied regenerated partitions BEFORE selection,
// including the required parent path, without replacing the stored evaluation.
func TestC2EstimationReplayPartitionComparisonContract(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		present, substituted bool
	}{
		{"quality_first_absent_partition_preserved_contract", false, false},
		{"fitness_band_stored_partition_equal_contract", true, false},
		{"fitness_band_valid_substituted_membership_contract", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := c2Bundle(3, EstimatorPartitionGroup{"g-a", []string{"a", "b"}}, EstimatorPartitionGroup{"g-b", []string{"c"}})
			if !tc.present {
				stored.Evaluation.EstimatorPartition = nil
			}
			before, _ := json.Marshal(stored.Evaluation)
			var regenerated EvaluationSnapshot
			_ = json.Unmarshal(before, &regenerated)
			if tc.substituted {
				regenerated.EstimatorPartition.Groups[0].CandidateIDs = []string{"a", "c"}
				regenerated.EstimatorPartition.Groups[1].CandidateIDs = []string{"b"}
			}
			if err := regenerated.ValidateAgainst(stored.Snapshot.Candidates); err != nil {
				t.Fatal(err)
			}
			left, _ := json.Marshal(stored.Evaluation.EstimatorPartition)
			right, _ := json.Marshal(regenerated.EstimatorPartition)
			result := ReplayResult{Differences: []MemberDifference{}}
			if !bytes.Equal(left, right) {
				result.Differences = append(result.Differences, MemberDifference{"$.routing.evaluation.estimator_partition", left, right})
			}
			result.Equal = len(result.Differences) == 0
			if result.Equal == tc.substituted {
				t.Fatalf("comparison=%+v", result)
			}
			after, _ := json.Marshal(stored.Evaluation)
			if !bytes.Equal(before, after) {
				t.Fatal("stored evaluation replaced before comparison")
			}
			if !tc.present && bytes.Contains(after, []byte("estimator_partition")) {
				t.Fatal("absent partition synthesized")
			}
			golden(t, "testdata/c2/"+tc.name+".json", encoded(t, result))
		})
	}
}

func TestC2Goldens(t *testing.T) {
	b := c2Bundle(2, EstimatorPartitionGroup{"opaque", []string{"a", "b"}})
	b.Snapshot.Inflight[0].Runs = 2
	if err := b.Evaluation.ValidateAgainst(b.Snapshot.Candidates); err != nil {
		t.Fatal(err)
	}
	routed, err := Route(b)
	if err != nil {
		t.Fatal(err)
	}
	if routed.Decision.SelectedCandidate.ID != "b" || routed.Decision.Alternatives[0].CandidateID != "a" || *routed.Decision.Alternatives[0].BasePosition != 0 {
		t.Fatal("headroom semantic order failed")
	}
	without := b
	without.Policy.Headroom.Enabled = false
	withResult, err := Route(without)
	if err != nil {
		t.Fatal(err)
	}
	without.Evaluation.EstimatorPartition = nil
	absentResult, err := Route(without)
	if err != nil || withResult.Decision.DecisionID == absentResult.Decision.DecisionID {
		t.Fatal("partition must change bound decision id even with identical order")
	}
	for _, tc := range []struct {
		name   string
		value  any
		digest func() (string, error)
	}{
		{"partition", b.Evaluation.EstimatorPartition, func() (string, error) { return canonical.Digest(b.Evaluation.EstimatorPartition) }},
		{"evaluation", b.Evaluation, b.Evaluation.Digest},
		{"decision", routed.Decision, routed.Decision.ContentID},
	} {
		golden(t, "testdata/c2/"+tc.name+".json", encoded(t, tc.value))
		raw, err := canonical.Marshal(tc.value)
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "testdata/c2/"+tc.name+".canonical.json", raw)
		d, err := tc.digest()
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "testdata/c2/"+tc.name+".digest", []byte(d+"\n"))
	}
}

func TestC2FitnessBandAllReservedAndBaseAbstention(t *testing.T) {
	b := c2Bundle(2, EstimatorPartitionGroup{"g", []string{"a", "b"}})
	for i := range b.Snapshot.UsageFacts {
		b.Snapshot.UsageFacts[i].Windows[0] = window(b, "session", 300, 9000, 1000)
	}
	b.Policy.Headroom.OnAllReserved = ReservedAbstain
	r, err := Route(b)
	if err != nil || r.Decision.Outcome != OutcomeAbstain {
		t.Fatalf("reserved route=%+v %v", r, err)
	}
	if _, err := ExplainDecision(b, *r.Decision); err != nil {
		t.Fatal(err)
	}
	base := fixedStrategy{result: SelectionResult{Abstention: &Abstention{Reason: ReasonQuotaUnknown}}}
	result, err := (HeadroomStrategy{Base: base}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil || result.Abstention == nil || result.Abstention.Reason != ReasonQuotaUnknown {
		t.Fatalf("base abstention=%+v %v", result, err)
	}
}

// Mode off must leave the caller's choice untouched (R7): even an invalid
// partition is not evaluated.
func TestC2ModeOffIgnoresPartition(t *testing.T) {
	b := c2Bundle(2, EstimatorPartitionGroup{"g-a", []string{"a"}}, EstimatorPartitionGroup{"g-b", []string{"b"}})
	b.Policy.Mode = ModeOff
	b.Versions.EstimatorVersion = "future"
	r, err := Route(b)
	if err != nil || r.EffectiveCandidate == nil || r.EffectiveCandidate.ID != b.Snapshot.ConfigOrder[0] {
		t.Fatalf("mode off must return the baseline without evaluating: %+v %v", r, err)
	}
	d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, fixedOrder(b, b.Snapshot.ConfigOrder), b.Versions)
	if err != nil || d != nil {
		t.Fatalf("mode off builds no decision: %+v %v", d, err)
	}
	raw, _ := json.Marshal(b)
	if _, err := LoadBundle(raw); err != nil {
		t.Fatalf("mode off bundle must load: %v", err)
	}
}

// Empty ids are structural even when ordering would also fail.
func TestC2PartitionEmptyIDsBeforeOrdering(t *testing.T) {
	b := c2Bundle(2, EstimatorPartitionGroup{"g-a", []string{"a"}}, EstimatorPartitionGroup{"g-b", []string{"b"}})
	cands := append([]ExecutionCandidate(nil), b.Snapshot.Candidates...)
	cands[1].ID = ""
	assertCode(t, b.Evaluation.EstimatorPartition.ValidateAgainst(cands, b.Evaluation.Estimates), ContractEmptyField)
	ests := append([]Estimate(nil), b.Evaluation.Estimates...)
	ests[1].CandidateID = ""
	assertCode(t, b.Evaluation.EstimatorPartition.ValidateAgainst(b.Snapshot.Candidates, ests), ContractEmptyField)
}
