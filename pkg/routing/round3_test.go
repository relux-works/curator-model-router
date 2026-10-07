package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func TestProfileDigestForms(t *testing.T) {
	for _, field := range []string{"lock_sha256", "weight_digest"} {
		for _, tc := range []struct {
			name, value, code string
		}{
			{"digest", "sha256:" + strings.Repeat("a", 64), ""},
			{"unknown", Unknown, ""},
			{"empty", "", ContractEmptyField},
			{"malformed", "bad", ContractInvalidDigest},
			{"short", "sha256:" + strings.Repeat("a", 63), ContractInvalidDigest},
			{"nonhex", "sha256:" + strings.Repeat("g", 64), ContractInvalidDigest},
			{"uppercase", "sha256:" + strings.Repeat("A", 64), ContractInvalidDigest},
			{"address", "obs:" + strings.Repeat("a", 64), ContractInvalidDigest},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				v := fixture[CandidateSnapshot](t, "candidates-rich")
				if field == "lock_sha256" {
					v.Candidates[0].ContextProfile.LockSHA256 = tc.value
				} else {
					v.Candidates[0].EngineProfile.WeightDigest = tc.value
				}
				_, err := v.Digest()
				assertCode(t, err, tc.code)
			})
		}
	}
}

func TestUsageFactRecordDigestForms(t *testing.T) {
	for _, state := range []State{StateExact, StatePercentOnly, StateLastObserved, StateNotSupported, StateUnavailable, StateAbsent} {
		for _, tc := range []struct {
			name  string
			value *string
			code  string
		}{
			{"omitted", nil, ContractMissingField},
			{"digest", ptr("sha256:" + strings.Repeat("a", 64)), ""},
			{"unknown", ptr(Unknown), ContractInvalidDigest},
			{"empty", ptr(""), ContractEmptyField},
			{"malformed", ptr("bad"), ContractInvalidDigest},
			{"short", ptr("sha256:" + strings.Repeat("a", 63)), ContractInvalidDigest},
			{"nonhex", ptr("sha256:" + strings.Repeat("g", 64)), ContractInvalidDigest},
			{"uppercase", ptr("sha256:" + strings.Repeat("A", 64)), ContractInvalidDigest},
			{"address", ptr("obs:" + strings.Repeat("a", 64)), ContractInvalidDigest},
		} {
			t.Run(string(state)+"/"+tc.name, func(t *testing.T) {
				v := fixture[CandidateSnapshot](t, "candidates-rich")
				v.UsageFacts[0].State = state
				v.UsageFacts[0].RecordDigest = tc.value
				v.UsageFacts[0].Windows = []UsageWindow{}
				code := tc.code
				if state == StateAbsent {
					code = ContractFieldForbidden
					if tc.value == nil {
						code = ""
					}
				}
				_, err := v.Digest()
				assertCode(t, err, code)
				if err != nil && !strings.Contains(err.Error(), "$.usage_facts[0].record_digest") {
					t.Fatalf("got=%v want record_digest path", err)
				}
				if state == StateAbsent && tc.value == nil {
					raw, err := json.Marshal(v.UsageFacts[0])
					if err != nil || bytes.Contains(raw, []byte(`"record_digest"`)) {
						t.Fatalf("got=%s err=%v want record_digest omitted", raw, err)
					}
				}
			})
		}
	}
}

func TestDecisionOrderingAndRetraction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*RoutingDecision)
		code   string
	}{
		{"decision reasons reversed", func(v *RoutingDecision) { v.ReasonCodes = []ReasonCode{ReasonQuotaUnknown, ReasonInflightUnknown} }, canonical.Unordered},
		{"alternative reasons reversed", func(v *RoutingDecision) {
			v.Alternatives[0].ReasonCodes = []ReasonCode{ReasonQuotaUnknown, ReasonInflightUnknown}
		}, canonical.Unordered},
		{"decision reasons duplicate", func(v *RoutingDecision) { v.ReasonCodes = []ReasonCode{ReasonQuotaUnknown, ReasonQuotaUnknown} }, canonical.DuplicateKey},
		{"alternative reasons duplicate", func(v *RoutingDecision) {
			v.Alternatives[0].ReasonCodes = []ReasonCode{ReasonQuotaUnknown, ReasonQuotaUnknown}
		}, canonical.DuplicateKey},
		{"retraction accepted", func(v *RoutingDecision) { v.EvidenceRefs = []string{"ret:" + strings.Repeat("c", 64)} }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := fixture[RoutingDecision](t, "decision")
			tc.mutate(&v)
			_, err := v.ContentID()
			assertCode(t, err, tc.code)
		})
	}
}

func TestTaskEnvelopeDigestValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*TaskEnvelope)
		code   string
	}{
		{"empty description", func(v *TaskEnvelope) { v.Description = "" }, ContractEmptyField},
		{"missing tools", func(v *TaskEnvelope) { v.Tools = nil }, ContractMissingField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := fixture[TaskEnvelope](t, "envelope")
			tc.mutate(&v)
			digest, err := v.Digest()
			assertCode(t, err, tc.code)
			if digest != "" {
				t.Fatalf("got digest=%q want empty on refusal", digest)
			}
		})
	}
}

func TestAbsentFactWindows(t *testing.T) {
	for _, state := range []State{StateAbsent, StateNotSupported, StateUnavailable} {
		for _, label := range []Freshness{Fresh, Stale, Expired, Invalid} {
			t.Run(string(state)+"/"+string(label), func(t *testing.T) {
				v := fixture[CandidateSnapshot](t, "candidates")
				fact := &v.UsageFacts[0]
				fact.State = state
				fact.Windows = fact.Windows[:1]
				fact.Windows[0].Freshness = label
				if label == Expired {
					fact.Windows[0].ResetsAt = ptr(v.AsOf)
				}
				code := ""
				if state == StateAbsent {
					fact.RecordDigest = nil
					code = ContractFieldForbidden
				}
				err := v.Validate()
				assertCode(t, err, code)
				if err != nil && !strings.Contains(err.Error(), "$.usage_facts[0].windows") {
					t.Fatalf("got=%v want windows path", err)
				}
			})
		}
	}
}

func TestWindowFilterInvalidUTF8(t *testing.T) {
	for _, id := range []string{"\xfe", "\xff", "valid-\xfe"} {
		for _, method := range []string{"direct", "json", "canonical", "policy digest"} {
			t.Run(fmt.Sprintf("%x/%s", id, method), func(t *testing.T) {
				filter := WindowFilter{IDs: []string{id}}
				var err error
				switch method {
				case "direct":
					_, err = filter.MarshalJSON()
				case "json":
					_, err = json.Marshal(filter)
				case "canonical":
					_, err = canonical.Marshal(struct {
						SchemaVersion string       `json:"schema_version"`
						Windows       WindowFilter `json:"windows"`
					}{"v1", filter})
				case "policy digest":
					v := DefaultPolicy("v1")
					v.Headroom.Windows = filter
					_, err = v.Digest()
				}
				assertCode(t, err, canonical.InvalidJSON)
			})
		}
	}
}

func TestContractMapFields(t *testing.T) {
	for _, field := range []string{"facets", "features"} {
		for _, tc := range []struct {
			name  string
			value map[string]string
			code  string
		}{
			{"empty key", map[string]string{"": "value"}, ContractEmptyField},
			{"empty value", map[string]string{"key": ""}, ContractEmptyField},
			{"unknown", map[string]string{Unknown: Unknown}, ""},
			{"value", map[string]string{"language": "go"}, ""},
			{"empty map", map[string]string{}, ""},
			{"omitted", nil, ""},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				v := fixture[EvaluationSnapshot](t, "evaluation")
				v.Assessments = []Assessment{{ID: "a", Requirements: Requirements{Items: []Requirement{{Category: "code.implement"}}}, AssessorVersion: "v1"}}
				if field == "facets" {
					v.Assessments[0].Requirements.Items[0].Facets = tc.value
				} else {
					v.Assessments[0].Features = tc.value
				}
				_, err := v.Digest()
				assertCode(t, err, tc.code)
				if err != nil && (!strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), `use "unknown"`)) {
					t.Fatalf("got=%v want map field and unknown guidance", err)
				}
			})
		}
	}
}

func TestSnapshotDigestForms(t *testing.T) {
	for _, field := range []string{"evidence_snapshot_digest", "source.digest", "evaluator_versions.digest", "evidence_ref.digest"} {
		for _, tc := range []struct{ name, value, code string }{
			{"digest", "sha256:" + strings.Repeat("a", 64), ""},
			{"unknown", Unknown, ContractInvalidDigest},
			{"malformed", "bad", ContractInvalidDigest},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				var err error
				switch field {
				case "evidence_snapshot_digest":
					v := fixture[EvaluationSnapshot](t, "evaluation")
					v.EvidenceSnapshotDigest = tc.value
					err = v.Validate()
				case "source.digest":
					v := fixture[CandidateSnapshot](t, "candidates-rich")
					v.Source.Digest = tc.value
					err = v.Validate()
				case "evaluator_versions.digest":
					v := fixture[EvaluationSnapshot](t, "evaluation")
					v.EvaluatorVersions[0].Digest = tc.value
					err = v.Validate()
				case "evidence_ref.digest":
					err = (EvidenceSnapshotRef{Digest: tc.value, RecordIDs: []string{}}).Validate()
				}
				assertCode(t, err, tc.code)
			})
		}
	}
}
