package routing

import (
	"reflect"
	"slices"
	"testing"
)

// Every accepted matrix decision must match the same Route stage and replay.
func assertQualityDecisionReplays(t *testing.T, b DecisionBundle, d *RoutingDecision) {
	t.Helper()
	comparison := b
	if d != nil && d.SelectionOrigin == OriginRouter {
		comparison.Versions.BaselineFallbackReasons = nil
	}
	routed, err := Route(comparison)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d, routed.Decision) {
		t.Fatalf("accepted decision differs from matching Route stage: built=%+v routed=%+v", d, routed.Decision)
	}
	replayed, err := Replay(b, d)
	if err != nil || !replayed.Equal {
		t.Fatalf("accepted decision did not replay equally: %+v %v", replayed, err)
	}
}

// Called by the common matrix for every base, headroom toggle and grouping source.
func testQualitySelectionConsistency(t *testing.T, makeBundle func() DecisionBundle) {
	t.Helper()
	for _, tc := range []struct {
		name   string
		mutate func(*SelectionResult)
	}{
		{"winner_order", func(r *SelectionResult) {
			r.Order[0], r.Order[1] = r.Order[1], r.Order[0]
			r.Order[0].Position, r.Order[1].Position = 0, 1
		}},
		{"alternative_order", func(r *SelectionResult) {
			r.Order[1], r.Order[2] = r.Order[2], r.Order[1]
			r.Order[1].Position, r.Order[2].Position = 1, 2
		}},
		{"winner_reasons", func(r *SelectionResult) { r.Order[0].ReasonCodes = []ReasonCode{} }},
		{"alternative_reasons", func(r *SelectionResult) { r.Order[1].ReasonCodes = []ReasonCode{} }},
		{"extra_reason", func(r *SelectionResult) {
			r.Order[0].ReasonCodes = append(slices.Clone(r.Order[0].ReasonCodes), ReasonCostUnknown)
		}},
		{"reason_order", func(r *SelectionResult) { slices.Reverse(r.Order[0].ReasonCodes) }},
		{"base_position_presence", func(r *SelectionResult) {
			present := r.Order[0].BasePosition != nil
			for i := range r.Order {
				if present {
					r.Order[i].BasePosition = nil
				} else {
					r.Order[i].BasePosition = ptr64(int64(i))
				}
			}
		}},
		{"base_position_values", func(r *SelectionResult) {
			if r.Order[0].BasePosition != nil {
				r.Order[0].BasePosition, r.Order[1].BasePosition = r.Order[1].BasePosition, r.Order[0].BasePosition
			}
		}},
		{"forged_abstention", func(r *SelectionResult) {
			*r = SelectionResult{Abstention: &Abstention{ReasonQualityFloorUnmet}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := makeBundle()
			original := selectBundle(t, b)
			changed := selectBundle(t, b)
			tc.mutate(&changed)
			if reflect.DeepEqual(original, changed) {
				// Unwrapped bases have one reason and no base positions to swap.
				if tc.name != "reason_order" && tc.name != "base_position_values" {
					t.Fatal("test mutation did not alter the result")
				}
				return
			}
			if err := changed.ValidateAgainst(b.Snapshot.Candidates); err != nil {
				t.Fatalf("mutation must remain structurally valid: %v", err)
			}
			d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, changed, b.Versions)
			// Assert the replay property even if the intended refusal regresses.
			if err == nil {
				assertQualityDecisionReplays(t, b, d)
			}
			assertCode(t, err, RoutingSelectionMismatch)
			if d != nil {
				t.Fatal("mismatch returned a decision")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*DecisionBundle)
		reason ReasonCode
	}{
		{"selected", func(*DecisionBundle) {}, ""},
		{"empty_admission", func(b *DecisionBundle) {
			b.Snapshot.Candidates, b.Snapshot.ConfigOrder = []ExecutionCandidate{}, []string{}
			b.Evaluation.Estimates = []Estimate{}
			if b.Evaluation.EstimatorPartition != nil {
				b.Evaluation.EstimatorPartition.Groups = []EstimatorPartitionGroup{}
			}
			if b.Evaluation.Costs != nil {
				b.Evaluation.Costs.Candidates = []CandidateCost{}
			}
		}, ""},
		{"floor_unmet", func(b *DecisionBundle) {
			for i := range b.Evaluation.Estimates {
				b.Evaluation.Estimates[i].Fitness = nil
			}
		}, ReasonQualityFloorUnmet},
		{"cost_unknown", func(b *DecisionBundle) {
			for i := range b.Evaluation.Costs.Candidates {
				b.Evaluation.Costs.Candidates[i].LatencyMS = nil
			}
		}, ReasonCostUnknown},
		{"cost_incomparable", func(b *DecisionBundle) { b.Policy.Cost.BillingOrder = []BillingClass{} }, ReasonCostIncomparable},
		{"headroom_reserve", func(b *DecisionBundle) {
			b.Envelope.Role = "developer"
			b.Policy.Headroom.OnAllReserved = ReservedAbstain
			setBilling(b, 3, BillingSubscription)
			for i := range b.Snapshot.UsageFacts {
				b.Snapshot.UsageFacts[i].Windows = []UsageWindow{window(*b, "session", 300, 9000, 1800)}
			}
		}, ReasonReserveBreached},
	} {
		b := makeBundle()
		if (tc.reason == ReasonQualityFloorUnmet && b.Policy.Quality == nil) ||
			((tc.reason == ReasonCostUnknown || tc.reason == ReasonCostIncomparable) && b.Policy.Cost == nil) ||
			(tc.reason == ReasonReserveBreached && !b.Policy.Headroom.Enabled) {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			retained := selectBundle(t, b)
			tc.mutate(&b)
			expected := selectBundle(t, b)
			if tc.reason != "" && (expected.Abstention == nil || expected.Abstention.Reason != tc.reason) {
				t.Fatalf("selection=%+v; want abstention %s", expected, tc.reason)
			}
			if tc.name == "empty_admission" && (len(expected.Order) != 0 || expected.Abstention != nil) {
				t.Fatalf("empty admission selection=%+v", expected)
			}
			d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, expected, b.Versions)
			if err != nil || d == nil {
				t.Fatalf("fresh result refused: d=%v err=%v", d, err)
			}
			assertQualityDecisionReplays(t, b, d)
			t.Run("fallback_boundary", func(t *testing.T) {
				testQualityFallbackBoundary(t, b, expected, d)
			})
			if expected.Abstention == nil && tc.name != "empty_admission" {
				return
			}
			for _, stale := range []struct {
				name   string
				result SelectionResult
			}{
				{"retained_selection", retained},
				{"wrong_abstention_reason", SelectionResult{Abstention: &Abstention{ReasonQuotaUnknown}}},
			} {
				if tc.name == "empty_admission" && stale.name == "retained_selection" {
					continue // The retained order no longer covers admission.
				}
				t.Run(stale.name, func(t *testing.T) {
					if err := stale.result.ValidateAgainst(b.Snapshot.Candidates); err != nil {
						t.Fatal(err)
					}
					accepted, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, stale.result, b.Versions)
					if err == nil {
						assertQualityDecisionReplays(t, b, accepted)
					}
					assertCode(t, err, RoutingSelectionMismatch)
					if accepted != nil {
						t.Fatal("stale result returned a decision")
					}
				})
			}
		})
	}
}

// Historical callers may supply a result independent of the built-in strategy.
// Keep this control alongside the existing core canonical-byte goldens.
func testCoreBuildDecisionSuppliedSelection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		b := fixtureBundle(3)
		b.Policy.Headroom.Enabled = enabled
		for _, r := range []SelectionResult{
			fixedOrder(b, []string{"c", "b", "a"}),
			{Abstention: &Abstention{ReasonQuotaUnknown}},
		} {
			d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
			if err != nil || d == nil {
				t.Fatalf("historical supplied result refused: headroom=%t d=%v err=%v", enabled, d, err)
			}
			if r.Abstention != nil {
				if d.Outcome != OutcomeAbstain || !slices.Equal(d.ReasonCodes, []ReasonCode{ReasonQuotaUnknown}) {
					t.Fatalf("historical abstention changed: %+v", d)
				}
			} else if d.Outcome != OutcomeSelected || d.SelectedCandidate.ID != "c" {
				t.Fatalf("historical supplied order changed: %+v", d)
			}
		}
	}
}

func TestQualityCompositionBaseAbstentionPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DecisionBundle)
		reason ReasonCode
	}{
		{"floor_unmet_without_partition", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition = nil
			for i := range b.Evaluation.Estimates {
				b.Evaluation.Estimates[i].Fitness = nil
			}
		}, ReasonQualityFloorUnmet},
		{"cost_unknown_without_partition", func(b *DecisionBundle) {
			b.Evaluation.EstimatorPartition = nil
			for i := range b.Evaluation.Costs.Candidates {
				b.Evaluation.Costs.Candidates[i].LatencyMS = nil
			}
		}, ReasonCostUnknown},
		{"incomparable_before_floor_conflict", func(b *DecisionBundle) {
			b.Policy.Cost.BillingOrder = []BillingClass{}
			b.Evaluation.Estimates[0].Fitness = ptr64(5999)
		}, ReasonCostIncomparable},
		{"incomparable_before_knownness_conflict", func(b *DecisionBundle) {
			b.Evaluation.Costs.Candidates[0].LatencyMS = nil
			b.Evaluation.Costs.Candidates[2].QuotaUnit = ptrString("tokens@1")
		}, ReasonCostIncomparable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := wrapperBundle(FitnessBand, StrategyCostWithQualityFloor)
			tc.mutate(&b)
			r := selectBundle(t, b)
			if r.Abstention == nil || r.Abstention.Reason != tc.reason {
				t.Fatalf("base abstention=%+v; want %s", r, tc.reason)
			}
			d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := Replay(b, d)
			if err != nil || !replayed.Equal {
				t.Fatalf("accepted abstention did not replay: %+v %v", replayed, err)
			}
		})
	}
}

func TestQualityCompositionSuppliedAbstentionCannotHideConflict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DecisionBundle)
		code   string
	}{
		{"floor", func(b *DecisionBundle) { b.Evaluation.Estimates[0].Fitness = ptr64(5999) }, HeadroomQualityFloorConflict},
		{"knownness", func(b *DecisionBundle) { b.Evaluation.Costs.Candidates[0].LatencyMS = nil }, HeadroomCostKnownnessConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := wrapperBundle(SamePairAnyHome, StrategyCostWithQualityFloor)
			tc.mutate(&b)
			r := SelectionResult{Abstention: &Abstention{ReasonReserveBreached}}
			_, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
			assertCode(t, err, tc.code)
		})
	}
}

// Exercise both recorded stages with identical frozen strategy inputs. This runs
// in the input matrix for all bases, headroom modes and equivalence sources.
func testQualityFallbackBoundary(t *testing.T, original DecisionBundle, r SelectionResult, before *RoutingDecision) {
	t.Helper()
	for _, tc := range []struct {
		name      string
		allowed   []ReasonCode
		mandatory bool
	}{
		{"absent", nil, false},
		{"allowed", slices.Clone(before.ReasonCodes), false},
		{"disallowed", []ReasonCode{ReasonQuotaUnknown}, false},
		{"mandatory_floor", slices.Clone(before.ReasonCodes), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := original
			b.Versions.BaselineFallbackReasons = tc.allowed
			b.Versions.MandatoryEvidenceFloor = tc.mandatory
			d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
			if err != nil || !reflect.DeepEqual(d, before) {
				t.Fatalf("fallback options changed pre-fallback decision: built=%+v before=%+v err=%v", d, before, err)
			}
			assertQualityDecisionReplays(t, b, d)
			ex, err := ExplainDecision(b, *d)
			if err != nil || ex.Outcome != d.Outcome || ex.SelectionOrigin != OriginRouter {
				t.Fatalf("pre-fallback explanation=%+v err=%v", ex, err)
			}
			routed, err := Route(b)
			attempt := d.Outcome == OutcomeAbstain && len(tc.allowed) > 0
			blocked := attempt && (tc.name == "disallowed" || tc.mandatory || b.Policy.Quality != nil)
			if blocked {
				assertCode(t, err, "routing_fallback_not_allowed")
			} else if err != nil {
				t.Fatal(err)
			}
			if d.Outcome != OutcomeAbstain {
				if !reflect.DeepEqual(routed.Decision, d) {
					t.Fatalf("non-abstention changed at fallback: %+v", routed)
				}
				return
			}
			if d.SelectionOrigin != OriginRouter || d.SelectedCandidate != nil {
				t.Fatalf("builder finalized abstention: %+v", d)
			}
			var baseline ExecutionCandidate
			for _, candidate := range b.Snapshot.Candidates {
				if candidate.ID == b.Snapshot.ConfigOrder[0] {
					baseline = candidate
				}
			}
			fallback, err := BaselineFallback(*d, b.Snapshot, baseline, tc.allowed, tc.mandatory)
			if !attempt || blocked {
				assertCode(t, err, "routing_fallback_not_allowed")
				if !attempt && !reflect.DeepEqual(routed.Decision, d) {
					t.Fatalf("unauthorized route changed abstention: %+v", routed)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fallback.Outcome != OutcomeSelected || fallback.SelectionOrigin != OriginBaselineAfterAbstain ||
				fallback.SelectedCandidate.ID != baseline.ID || fallback.DecisionID == d.DecisionID ||
				!slices.Equal(fallback.ReasonCodes, d.ReasonCodes) || !reflect.DeepEqual(routed.Decision, &fallback) {
				t.Fatalf("fallback transition differs: before=%+v fallback=%+v route=%+v", d, fallback, routed)
			}
			if err := fallback.VerifyContentID(); err != nil {
				t.Fatal(err)
			}
			if b.Policy.Mode == ModeSelect && !reflect.DeepEqual(routed.EffectiveCandidate, &baseline) {
				t.Fatalf("fallback not effective: %+v", routed)
			}
			assertQualityDecisionReplays(t, b, &fallback)
			ex, err = ExplainDecision(b, fallback)
			if err != nil || ex.Outcome != OutcomeSelected || ex.SelectionOrigin != OriginBaselineAfterAbstain || ex.SelectedCandidateID != baseline.ID {
				t.Fatalf("fallback explanation=%+v err=%v", ex, err)
			}
			// The public fallback seam can record another admitted baseline. Replay
			// must use that exact variant, not substitute Route's config baseline.
			t.Run("explicit_baseline", func(t *testing.T) {
				otherBaseline := b.Snapshot.Candidates[1]
				if otherBaseline.ID == baseline.ID {
					t.Fatal("explicit baseline must differ from Route baseline")
				}
				direct, err := BaselineFallback(*d, b.Snapshot, otherBaseline, tc.allowed, false)
				if err != nil {
					t.Fatal(err)
				}
				replayed, err := Replay(b, &direct)
				if err != nil || !replayed.Equal {
					t.Fatalf("explicit baseline did not replay: %+v err=%v", replayed, err)
				}
				ex, err := ExplainDecision(b, direct)
				if err != nil || ex.SelectedCandidateID != otherBaseline.ID || ex.SelectionOrigin != OriginBaselineAfterAbstain {
					t.Fatalf("explicit baseline explanation=%+v err=%v", ex, err)
				}
				foreign := otherBaseline
				foreign.Model = "foreign"
				direct.SelectedCandidate = &foreign
				direct, err = direct.WithContentID()
				if err != nil {
					t.Fatal(err)
				}
				_, err = Replay(b, &direct)
				assertCode(t, err, "routing_fallback_not_allowed")
			})
			// A finalized record needs its original authorization and floor inputs.
			for _, changed := range []struct {
				name      string
				allowed   []ReasonCode
				mandatory bool
				code      string
			}{
				{"removed", nil, false, "routing_fallback_not_allowed"},
				{"wrong_reason", []ReasonCode{ReasonQuotaUnknown}, false, "routing_fallback_not_allowed"},
				{"floor_added", tc.allowed, true, "routing_fallback_not_allowed"},
			} {
				t.Run(changed.name, func(t *testing.T) {
					other := b
					other.Versions.BaselineFallbackReasons = changed.allowed
					other.Versions.MandatoryEvidenceFloor = changed.mandatory
					replayed, err := Replay(other, &fallback)
					if changed.code != "" {
						assertCode(t, err, changed.code)
					} else if err != nil || replayed.Equal {
						t.Fatalf("missing fallback inputs replayed: %+v err=%v", replayed, err)
					}
				})
			}
		})
	}
}

func TestCorePreFallbackReplayRetainsHistoricalBehavior(t *testing.T) {
	b := fixtureBundle(2)
	b.Policy.Headroom.OnAllReserved = ReservedAbstain
	for i := range b.Snapshot.UsageFacts {
		b.Snapshot.UsageFacts[i].Windows = []UsageWindow{window(b, "session", 300, 9000, 1000)}
	}
	b.Versions.BaselineFallbackReasons = []ReasonCode{ReasonReserveBreached}
	r := selectBundle(t, b)
	if r.Abstention == nil {
		t.Fatal("core control must abstain")
	}
	d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := Replay(b, d)
	if err != nil || replayed.Equal {
		t.Fatalf("historical core pre-fallback replay changed: %+v err=%v", replayed, err)
	}
}
