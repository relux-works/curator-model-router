package routing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func TestScopeMapOrdering(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []ScopeMapEntry
		code    string
	}{
		{"same runtime reversed scopes", []ScopeMapEntry{{"runtime-a", "z", []string{"a"}}, {"runtime-a", "a", []string{"b"}}}, canonical.Unordered},
		{"same runtime duplicate scope different models", []ScopeMapEntry{{"runtime-a", "a", []string{"a"}}, {"runtime-a", "a", []string{"b"}}}, canonical.DuplicateKey},
		{"models unordered", []ScopeMapEntry{{"runtime-a", "a", []string{"b", "a"}}}, canonical.Unordered},
		{"models duplicate", []ScopeMapEntry{{"runtime-a", "a", []string{"a", "a"}}}, canonical.DuplicateKey},
		{"models nonadjacent duplicate", []ScopeMapEntry{{"runtime-a", "a", []string{"a", "b", "a"}}}, canonical.DuplicateKey},
		{"ordered scopes and models", []ScopeMapEntry{{"runtime-a", "a", []string{"a", "b"}}, {"runtime-a", "b", []string{}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture[CandidateSnapshot](t, "candidates")
			s.ScopeMap.Entries = tc.entries
			_, err := s.Digest()
			assertCode(t, err, tc.code)
		})
	}
}

// Each required collection in every DTO is independently removed, including
// nested rubric/assessment requirements, group slices and alternative reasons.
func TestRequiredCollections(t *testing.T) {
	cases := []struct {
		name     string
		value    any
		validate func(any) error
	}{
		{"envelope", fixture[TaskEnvelope](t, "envelope"), func(v any) error { return v.(TaskEnvelope).Validate() }},
		{"candidates", fixture[CandidateSnapshot](t, "candidates-rich"), func(v any) error { return v.(CandidateSnapshot).Validate() }},
		{"evaluation", fixture[EvaluationSnapshot](t, "evaluation"), func(v any) error { return v.(EvaluationSnapshot).Validate() }},
		{"decision", fixture[RoutingDecision](t, "decision"), func(v any) error { return v.(RoutingDecision).Validate() }},
		{"policy", fixture[RoutingPolicy](t, "policy-nondefault"), func(v any) error { return v.(RoutingPolicy).Validate() }},
		{"requirements", Requirements{Items: []Requirement{{Category: "code"}}}, func(v any) error { return v.(Requirements).Validate() }},
		{"evidence ref", EvidenceSnapshotRef{Digest: "sha256:" + strings.Repeat("a", 64), RecordIDs: []string{}}, func(v any) error { return v.(EvidenceSnapshotRef).Validate() }},
		{"estimate", fixture[EvaluationSnapshot](t, "evaluation").Estimates[0], func(v any) error { return v.(Estimate).Validate() }},
		{"assessment requirements", Assessment{ID: "a", Requirements: Requirements{Items: []Requirement{}}, AssessorVersion: "v1"}, validateFields},
		{"selection reasons", SelectionResult{Order: []RankedCandidate{{CandidateID: "a", ReasonCodes: []ReasonCode{}}}}, func(v any) error { return v.(SelectionResult).ValidateAgainst([]ExecutionCandidate{{ID: "a"}}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(tc.value); err != nil {
				t.Fatalf("valid fixture: %v", err)
			}
			testNilCollections(t, reflect.ValueOf(tc.value), "$", tc.validate, tc.value)
		})
	}
	p := DefaultPolicy("v1")
	p.Headroom.Windows = WindowFilter{}
	err := p.Validate()
	assertCode(t, err, ContractMissingField)
	if !strings.Contains(err.Error(), "$.headroom.windows") {
		t.Fatalf("got=%v want field path", err)
	}
}

func testNilCollections(t *testing.T, v reflect.Value, path string, validate func(any) error, root any) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			testNilCollections(t, v.Elem(), path, validate, root)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			tag := strings.Split(v.Type().Field(i).Tag.Get("json"), ",")
			if tag[0] == "" || tag[0] == "-" {
				continue
			}
			field := v.Field(i)
			fieldPath := path + "." + tag[0]
			if field.Kind() == reflect.Slice && !slices.Contains(tag[1:], "omitempty") {
				// Clone through JSON, then locate the same field path in the clone.
				t.Run(fieldPath, func(t *testing.T) {
					clone := reflect.New(reflect.TypeOf(root))
					raw, err := json.Marshal(root)
					if err != nil {
						t.Fatal(err)
					}
					if err = json.Unmarshal(raw, clone.Interface()); err != nil {
						t.Fatal(err)
					}
					target := fieldAtPath(clone.Elem(), fieldPath)
					target.Set(reflect.Zero(target.Type()))
					err = validate(clone.Elem().Interface())
					assertCode(t, err, ContractMissingField)
					if !strings.Contains(err.Error(), fieldPath) {
						t.Fatalf("got=%v want path=%s", err, fieldPath)
					}
				})
			}
			testNilCollections(t, field, fieldPath, validate, root)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			testNilCollections(t, v.Index(i), fmt.Sprintf("%s[%d]", path, i), validate, root)
		}
	}
}
func fieldAtPath(v reflect.Value, path string) reflect.Value {
	for _, part := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		name, indexText, hasIndex := strings.Cut(part, "[")
		for i := 0; i < v.NumField(); i++ {
			if strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0] == name {
				v = v.Field(i)
				break
			}
		}
		if hasIndex {
			index, _ := strconv.Atoi(strings.TrimSuffix(indexText, "]"))
			v = v.Index(index)
		}
	}
	return v
}

func TestFreshnessLabelConsistency(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*UsageWindow)
	}{
		{"minutes low", func(w *UsageWindow) { w.Minutes = MinMinutes - 1 }},
		{"minutes high", func(w *UsageWindow) { w.Minutes = MaxMinutes + 1 }},
		{"used absent", func(w *UsageWindow) { w.UsedBP = nil }},
		{"used low", func(w *UsageWindow) { w.UsedBP = ptr(MinUsedBP - 1) }},
		{"used high", func(w *UsageWindow) { w.UsedBP = ptr(MaxUsedBP + 1) }},
		{"observation absent", func(w *UsageWindow) { w.ObservedAt = nil }},
		{"observation future", func(w *UsageWindow) { w.ObservedAt = ptr(int64(1800000001)) }},
		{"observation low", func(w *UsageWindow) { w.ObservedAt = ptr(MinTimestamp - 1) }},
		{"observation high", func(w *UsageWindow) { w.ObservedAt = ptr(MaxTimestamp + 1) }},
		{"reset low", func(w *UsageWindow) { w.ResetsAt = ptr(MinTimestamp - 1) }},
		{"reset high", func(w *UsageWindow) { w.ResetsAt = ptr(MaxTimestamp + 1) }},
	}
	for _, label := range []Freshness{Fresh, Stale, Expired} {
		for _, tc := range mutations {
			t.Run(string(label)+"/"+tc.name, func(t *testing.T) {
				s := fixture[CandidateSnapshot](t, "candidates")
				w := &s.UsageFacts[0].Windows[0]
				w.Freshness = label
				if label == Expired {
					w.ResetsAt = ptr(s.AsOf)
				}
				tc.mutate(w)
				assertCode(t, s.Validate(), ContractFreshnessMismatch)
			})
		}
		for _, reset := range []*int64{nil, ptr(int64(1799999999)), ptr(int64(1800000000)), ptr(int64(1800000001))} {
			t.Run(string(label)+"/reset="+fmt.Sprint(reset), func(t *testing.T) {
				s := fixture[CandidateSnapshot](t, "candidates")
				w := &s.UsageFacts[0].Windows[0]
				w.Freshness = label
				w.ResetsAt = reset
				code := ""
				expired := reset != nil && *reset <= s.AsOf
				if (label == Expired) != expired {
					code = ContractFreshnessMismatch
				}
				assertCode(t, s.Validate(), code)
			})
		}
	}
	s := fixture[CandidateSnapshot](t, "candidates")
	s.UsageFacts[0].Windows[0].Freshness = Invalid
	s.UsageFacts[0].Windows[0].ObservedAt = nil
	assertCode(t, s.Validate(), "")
}

func TestDecisionConsistency(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*RoutingDecision)
		code   string
	}{
		{"selected without candidate", func(v *RoutingDecision) { v.SelectedCandidate = nil }, "routing_invalid_decision"},
		{"abstain with candidate", func(v *RoutingDecision) { v.Outcome = OutcomeAbstain }, "routing_invalid_decision"},
		{"no eligible with candidate", func(v *RoutingDecision) { v.Outcome = OutcomeNoEligibleCandidates }, "routing_invalid_decision"},
		{"timeout with candidate", func(v *RoutingDecision) { v.Outcome = OutcomeTimeout }, "routing_invalid_decision"},
		{"invalid response with candidate", func(v *RoutingDecision) { v.Outcome = OutcomeInvalidResponse }, "routing_invalid_decision"},
		{"baseline abstain", func(v *RoutingDecision) {
			v.Outcome = OutcomeAbstain
			v.SelectedCandidate = nil
			v.SelectionOrigin = OriginBaselineAfterAbstain
		}, "routing_invalid_decision"},
		{"baseline selected", func(v *RoutingDecision) { v.SelectionOrigin = OriginBaselineAfterAbstain }, ""},
		{"expiry equal", func(v *RoutingDecision) { v.ExpiresAt = ptr(v.PreparedAt) }, "routing_invalid_decision"},
		{"expiry before", func(v *RoutingDecision) { v.ExpiresAt = ptr(v.PreparedAt - 1) }, "routing_invalid_decision"},
		{"expiry after", func(v *RoutingDecision) { v.ExpiresAt = ptr(v.PreparedAt + 1) }, ""},
		{"expiry absent", func(v *RoutingDecision) { v.ExpiresAt = nil }, ""},
		{"prepared low", func(v *RoutingDecision) { v.PreparedAt = MinTimestamp - 1 }, "routing_invalid_decision"},
		{"expiry high", func(v *RoutingDecision) { v.ExpiresAt = ptr(MaxTimestamp + 1) }, "routing_invalid_decision"},
		{"alternatives reversed", func(v *RoutingDecision) {
			v.Alternatives = append(v.Alternatives, RankedCandidate{CandidateID: "c", Position: 0, ReasonCodes: []ReasonCode{}})
		}, canonical.Unordered},
		{"alternatives duplicate position", func(v *RoutingDecision) {
			v.Alternatives = append(v.Alternatives, RankedCandidate{CandidateID: "c", Position: 1, ReasonCodes: []ReasonCode{}})
		}, canonical.DuplicateKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := fixture[RoutingDecision](t, "decision")
			tc.mutate(&v)
			assertCode(t, v.Validate(), tc.code)
		})
	}
	v := fixture[RoutingDecision](t, "decision")
	bound, err := v.WithContentID()
	if err != nil {
		t.Fatal(err)
	}
	assertCode(t, bound.VerifyContentID(), "")
	assertCode(t, v.VerifyContentID(), ContractDecisionIDMismatch)
	bound.DecisionID = "garbage"
	assertCode(t, bound.Validate(), ContractInvalidDigest)
	assertCode(t, bound.VerifyContentID(), ContractDecisionIDMismatch)
	bound.DecisionID = "sha256:" + strings.Repeat("0", 64)
	assertCode(t, bound.VerifyContentID(), ContractDecisionIDMismatch)
	bound, err = v.WithContentID()
	if err != nil {
		t.Fatal(err)
	}
	bound.ApplicabilityScope = "attempt-2"
	assertCode(t, bound.VerifyContentID(), ContractDecisionIDMismatch)
}

func TestDecisionDigestFields(t *testing.T) {
	for _, field := range []string{"TaskEnvelopeDigest", "RoleContextDigest", "CandidateSnapshotDigest", "EvaluationSnapshotDigest", "RoutingPolicyDigest", "DecisionID"} {
		for _, value := range []string{"unknown", "bad", "sha256:" + strings.Repeat("a", 63), "sha256:" + strings.Repeat("g", 64), "sha256:" + strings.Repeat("A", 64), "obs:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("a", 64)} {
			t.Run(field+"/"+value, func(t *testing.T) {
				v := fixture[RoutingDecision](t, "decision")
				reflect.ValueOf(&v).Elem().FieldByName(field).SetString(value)
				code := ContractInvalidDigest
				if value == "sha256:"+strings.Repeat("a", 64) {
					code = ""
				}
				assertCode(t, v.Validate(), code)
			})
		}
	}
}
func TestRequiredStringUnknown(t *testing.T) {
	for _, effort := range []string{"", "unknown", "High"} {
		t.Run(effort, func(t *testing.T) {
			s := fixture[CandidateSnapshot](t, "candidates")
			s.Candidates[0].Effort = effort
			err := s.Validate()
			code := ""
			if effort == "" {
				code = ContractEmptyField
			}
			assertCode(t, err, code)
			if effort == "" && (!strings.Contains(err.Error(), `use "unknown"`) || !strings.Contains(err.Error(), "$.candidates[0].effort")) {
				t.Fatalf("got=%v want field and unknown guidance", err)
			}
		})
	}
}
func TestEstimateBoundaries(t *testing.T) {
	for _, field := range []string{"Fitness", "Coverage"} {
		for _, value := range []int64{-1, 0, 1, 9999, 10000, 10001} {
			t.Run(fmt.Sprintf("%s/%d", field, value), func(t *testing.T) {
				v := fixture[EvaluationSnapshot](t, "evaluation")
				reflect.ValueOf(&v.Estimates[0]).Elem().FieldByName(field).Set(reflect.ValueOf(ptr(value)))
				code := ""
				if value < 0 || value > 10000 {
					code = "routing_invalid_estimate"
				}
				assertCode(t, v.Validate(), code)
			})
		}
	}
}
func TestRicherContractGoldens(t *testing.T) {
	candidates := fixture[CandidateSnapshot](t, "candidates-rich")
	policy, err := LoadPolicy(readFixture(t, "policy-nondefault.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		value  any
		digest func() (string, error)
	}{{"candidates-rich", candidates, candidates.Digest}, {"policy-nondefault", policy, policy.Digest}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonical.Marshal(tc.value)
			want := readFixture(t, tc.name+".canonical.json")
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("got=%s err=%v want=%s", got, err, want)
			}
			digest, err := tc.digest()
			wantDigest := strings.TrimSpace(string(readFixture(t, tc.name+".digest")))
			if err != nil || digest != wantDigest {
				t.Fatalf("got=%s err=%v want=%s", digest, err, wantDigest)
			}
		})
	}
}
func TestEstimatorMismatchError(t *testing.T) {
	err := NewEstimatorSnapshotMismatchError()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "estimator_snapshot_mismatch" {
		t.Fatalf("got=%v want typed estimator_snapshot_mismatch", err)
	}
}

func TestKnownEmptyCollections(t *testing.T) {
	envelope := TaskEnvelope{SchemaVersion: "v1", Description: Unknown, SubstantiveRevision: Unknown, Role: Unknown, Criteria: []string{}, Context: []string{}, Tools: []string{}}
	if _, err := envelope.Digest(); err != nil {
		t.Fatalf("known-empty envelope: %v", err)
	}
	snapshot := CandidateSnapshot{SchemaVersion: "v1", AsOf: 1800000000, ScopeMap: ScopeMap{Version: "v1", Entries: []ScopeMapEntry{}}, UsageFacts: []UsageFact{}, Inflight: []Inflight{}, Candidates: []ExecutionCandidate{}, ConfigOrder: []string{}, Source: Provenance{Name: Unknown}}
	if _, err := snapshot.Digest(); err != nil {
		t.Fatalf("known-empty snapshot: %v", err)
	}
	evaluation := EvaluationSnapshot{SchemaVersion: "v1", EvidenceSnapshotDigest: "sha256:" + strings.Repeat("0", 64), Measurements: []string{}, Assessments: []Assessment{}, Estimates: []Estimate{}, EvaluatorVersions: []Provenance{}}
	if _, err := evaluation.Digest(); err != nil {
		t.Fatalf("known-empty evaluation: %v", err)
	}
}
