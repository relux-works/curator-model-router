package routing

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func fixture[T any](t *testing.T, name string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(readFixture(t, name+".json"), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestContractDigests(t *testing.T) {
	envelope := fixture[TaskEnvelope](t, "envelope")
	candidates := fixture[CandidateSnapshot](t, "candidates")
	evaluation := fixture[EvaluationSnapshot](t, "evaluation")
	policy, err := LoadPolicy(readFixture(t, "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	decision := fixture[RoutingDecision](t, "decision")
	for _, tc := range []struct {
		name   string
		digest func() (string, error)
	}{{"envelope", envelope.Digest}, {"candidates", candidates.Digest}, {"evaluation", evaluation.Digest}, {"policy", policy.Digest}, {"decision", decision.ContentID}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.digest()
			want := strings.TrimSpace(string(readFixture(t, tc.name+".digest")))
			if err != nil || got != want {
				t.Fatalf("got=%s err=%v want=%s", got, err, want)
			}
			again, err := tc.digest()
			if err != nil || again != got {
				t.Fatalf("repeat got=%s err=%v want=%s", again, err, got)
			}
		})
	}
	id, err := decision.ContentID()
	if err != nil {
		t.Fatal(err)
	}
	decision.DecisionID = "arbitrary-old-id"
	again, err := decision.ContentID()
	if err != nil || again != id {
		t.Fatalf("id must exclude old id: got=%s err=%v want=%s", again, err, id)
	}
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var routingError *Error
	var canonicalError *canonical.Error
	got := ""
	if errors.As(err, &routingError) {
		got = routingError.Code
	} else if errors.As(err, &canonicalError) {
		got = canonicalError.Code
	}
	if got != code {
		t.Fatalf("got error=%v code=%q want code=%q", err, got, code)
	}
}
func TestSnapshotValidation(t *testing.T) {
	for _, bound := range []int64{MinTimestamp, MaxTimestamp} {
		for _, delta := range []int64{-1, 0, 1} {
			s := fixture[CandidateSnapshot](t, "candidates")
			s.AsOf = bound + delta
			s.UsageFacts = []UsageFact{}
			err := s.Validate()
			if s.AsOf < MinTimestamp || s.AsOf > MaxTimestamp {
				assertCode(t, err, "routing_invalid_as_of")
			} else if err != nil {
				t.Fatalf("as_of=%d err=%v", s.AsOf, err)
			}
		}
	}
	tests := []struct {
		name   string
		mutate func(*CandidateSnapshot)
		code   string
	}{
		{"candidates unordered", func(s *CandidateSnapshot) { s.Candidates[0], s.Candidates[1] = s.Candidates[1], s.Candidates[0] }, canonical.Unordered},
		{"candidates duplicate", func(s *CandidateSnapshot) { s.Candidates[1].ID = s.Candidates[0].ID }, canonical.DuplicateKey},
		{"facts unordered", func(s *CandidateSnapshot) { s.UsageFacts[0], s.UsageFacts[1] = s.UsageFacts[1], s.UsageFacts[0] }, canonical.Unordered},
		{"facts duplicate", func(s *CandidateSnapshot) { s.UsageFacts[1].Key = s.UsageFacts[0].Key }, canonical.DuplicateKey},
		{"windows unordered", func(s *CandidateSnapshot) {
			s.UsageFacts[0].Windows[0], s.UsageFacts[0].Windows[1] = s.UsageFacts[0].Windows[1], s.UsageFacts[0].Windows[0]
		}, canonical.Unordered},
		{"windows duplicate", func(s *CandidateSnapshot) { s.UsageFacts[0].Windows[1].ID = s.UsageFacts[0].Windows[0].ID }, canonical.DuplicateKey},
		{"inflight unordered", func(s *CandidateSnapshot) { s.Inflight[0], s.Inflight[1] = s.Inflight[1], s.Inflight[0] }, canonical.Unordered},
		{"inflight duplicate", func(s *CandidateSnapshot) { s.Inflight[1].Key = s.Inflight[0].Key }, canonical.DuplicateKey},
		{"scope unordered", func(s *CandidateSnapshot) {
			s.ScopeMap.Entries[0], s.ScopeMap.Entries[1] = s.ScopeMap.Entries[1], s.ScopeMap.Entries[0]
		}, canonical.Unordered},
		{"scope duplicate", func(s *CandidateSnapshot) { s.ScopeMap.Entries[1] = s.ScopeMap.Entries[0] }, canonical.DuplicateKey},
		{"unknown state", func(s *CandidateSnapshot) { s.UsageFacts[0].State = "future" }, "routing_unknown_enum"},
		{"unknown freshness", func(s *CandidateSnapshot) { s.UsageFacts[0].Windows[0].Freshness = "future" }, "routing_unknown_enum"},
		{"unknown billing", func(s *CandidateSnapshot) { s.Candidates[0].BillingClass = "future" }, "routing_unknown_enum"},
		{"missing usage key", func(s *CandidateSnapshot) { s.Candidates[0].UsageKey = nil }, "routing_missing_usage_key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture[CandidateSnapshot](t, "candidates")
			tc.mutate(&s)
			_, err := s.Digest()
			assertCode(t, err, tc.code)
		})
	}
}
func TestUnknownVersusZero(t *testing.T) {
	zero := int64(0)
	a := Estimate{CandidateID: "a", ContributingRecordIDs: []string{}, EstimatorVersion: "v1"}
	b := a
	b.Fitness = &zero
	b.Coverage = &zero
	aa, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(aa, bb) || bytes.Contains(aa, []byte(`"fitness"`)) || !bytes.Contains(bb, []byte(`"fitness":0`)) {
		t.Fatalf("absent=%s zero=%s", aa, bb)
	}
	w := UsageWindow{ID: "s", Scope: "all", Minutes: 1, UsedBP: ptr(int64(0)), Freshness: Invalid}
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"used_bp":0`)) || bytes.Contains(raw, []byte(`"resets_at"`)) {
		t.Fatalf("got=%s", raw)
	}
	w.UsedBP = nil
	raw, err = json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"used_bp"`)) {
		t.Fatalf("unknown used_bp must be omitted: %s", raw)
	}
}
func TestEvidenceReferences(t *testing.T) {
	for _, ref := range []string{"obs:" + strings.Repeat("a", 64), "note:" + strings.Repeat("b", 64)} {
		if err := checkAddresses([]string{ref}, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{"ret:" + strings.Repeat("c", 64), "obs:bad", "note:" + strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 64)} {
		assertCode(t, checkAddresses([]string{ref}, false), "routing_invalid_evidence_ref")
	}
	if err := checkAddresses([]string{"ret:" + strings.Repeat("c", 64)}, true); err != nil {
		t.Fatal(err)
	}
}
func TestSelectionResult(t *testing.T) {
	candidates := []ExecutionCandidate{{ID: "a"}, {ID: "b"}}
	tests := []struct {
		name   string
		result SelectionResult
		bad    bool
	}{
		{"ordered", SelectionResult{Order: []RankedCandidate{{CandidateID: "b", Position: 0, ReasonCodes: []ReasonCode{}}, {CandidateID: "a", Position: 1, ReasonCodes: []ReasonCode{}}}}, false},
		{"abstain", SelectionResult{Abstention: &Abstention{Reason: ReasonQuotaUnknown}}, false},
		{"partial", SelectionResult{Order: []RankedCandidate{{CandidateID: "a", Position: 0, ReasonCodes: []ReasonCode{}}}}, true},
		{"duplicate", SelectionResult{Order: []RankedCandidate{{CandidateID: "a", Position: 0, ReasonCodes: []ReasonCode{}}, {CandidateID: "a", Position: 1, ReasonCodes: []ReasonCode{}}}}, true},
		{"bad position", SelectionResult{Order: []RankedCandidate{{CandidateID: "a", Position: 1, ReasonCodes: []ReasonCode{}}, {CandidateID: "b", Position: 0, ReasonCodes: []ReasonCode{}}}}, true},
		{"both", SelectionResult{Order: []RankedCandidate{{CandidateID: "a", ReasonCodes: []ReasonCode{}}}, Abstention: &Abstention{Reason: ReasonQuotaUnknown}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.result.ValidateAgainst(candidates)
			if tc.bad {
				assertCode(t, err, "routing_invalid_selection")
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEvaluationValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EvaluationSnapshot)
		code   string
	}{
		{"negative fitness", func(v *EvaluationSnapshot) { v.Estimates[0].Fitness = ptr(int64(-1)) }, "routing_invalid_estimate"},
		{"high coverage", func(v *EvaluationSnapshot) { v.Estimates[0].Coverage = ptr(int64(10001)) }, "routing_invalid_estimate"},
		{"duplicate estimate", func(v *EvaluationSnapshot) { v.Estimates = append(v.Estimates, v.Estimates[0]) }, canonical.DuplicateKey},
		{"record order", func(v *EvaluationSnapshot) {
			refs := v.Estimates[0].ContributingRecordIDs
			refs[0], refs[1] = refs[1], refs[0]
		}, canonical.Unordered},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := fixture[EvaluationSnapshot](t, "evaluation")
			tc.mutate(&v)
			_, err := v.Digest()
			assertCode(t, err, tc.code)
		})
	}
}
func TestDecisionIdentityAndOutcomes(t *testing.T) {
	v := fixture[RoutingDecision](t, "decision")
	bound, err := v.WithContentID()
	if err != nil {
		t.Fatal(err)
	}
	want, err := v.ContentID()
	if err != nil {
		t.Fatal(err)
	}
	if v.DecisionID != "" || bound.DecisionID != want {
		t.Fatalf("original=%q bound=%q want=%q", v.DecisionID, bound.DecisionID, want)
	}
	bound.SelectedCandidate.Effort = "high"
	changed, err := bound.ContentID()
	if err != nil {
		t.Fatal(err)
	}
	if changed == want {
		t.Fatal("verbatim effort change must alter decision identity")
	}
	seen := map[string]bool{}
	for _, outcome := range []OutcomeKind{OutcomeAbstain, OutcomeNoEligibleCandidates, OutcomeTimeout, OutcomeInvalidResponse} {
		v := fixture[RoutingDecision](t, "decision")
		v.Outcome = outcome
		v.SelectedCandidate = nil
		id, err := v.ContentID()
		if err != nil {
			t.Fatalf("outcome=%s err=%v", outcome, err)
		}
		if seen[id] {
			t.Fatalf("outcome=%s shares digest %s", outcome, id)
		}
		seen[id] = true
	}
	v = fixture[RoutingDecision](t, "decision")
	v.Outcome = "future"
	_, err = v.ContentID()
	assertCode(t, err, "routing_unknown_enum")
}
