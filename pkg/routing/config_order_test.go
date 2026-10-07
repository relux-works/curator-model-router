package routing

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func TestConfigOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []string
		empty bool
		code  string
		id    string
	}{
		{"missing field", nil, false, ContractMissingField, "$.config_order"},
		{"extra id", []string{"b", "a", "extra"}, false, ContractConfigOrderMismatch, `"extra"`},
		{"replaced id", []string{"a", "extra"}, false, ContractConfigOrderMismatch, `"extra"`},
		{"missing id", []string{"a"}, false, ContractConfigOrderMismatch, `"b"`},
		{"empty order", []string{}, false, ContractConfigOrderMismatch, `"a"`},
		{"duplicate id", []string{"a", "a"}, false, ContractConfigOrderMismatch, `"a"`},
		{"nonadjacent duplicate", []string{"a", "b", "a"}, false, ContractConfigOrderMismatch, `"a"`},
		{"sorted order", []string{"a", "b"}, false, "", ""},
		{"semantic order", []string{"b", "a"}, false, "", ""},
		{"known empty", []string{}, true, "", ""},
		{"empty candidates extra id", []string{"extra"}, true, ContractConfigOrderMismatch, `"extra"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := fixture[CandidateSnapshot](t, "candidates")
			v.ConfigOrder = tc.order
			if tc.empty {
				v.Candidates = []ExecutionCandidate{}
			}
			before := append([]string(nil), v.ConfigOrder...)
			assertCode(t, v.Validate(), tc.code)
			digest, err := v.Digest()
			assertCode(t, err, tc.code)
			if tc.code != "" {
				var typed *Error
				if !errors.As(err, &typed) || !strings.Contains(typed.Message, tc.id) || digest != "" {
					t.Fatalf("digest=%q error=%v want typed refusal naming %s", digest, err, tc.id)
				}
			} else if !reflect.DeepEqual(before, append([]string(nil), v.ConfigOrder...)) {
				t.Fatal("validation or hashing changed semantic order")
			}
		})
	}
}

func TestConfigOrderDigestBinding(t *testing.T) {
	v := fixture[CandidateSnapshot](t, "candidates")
	initial, err := v.Digest()
	if err != nil {
		t.Fatal(err)
	}
	v.ConfigOrder = []string{v.ConfigOrder[1], v.ConfigOrder[0]}
	reordered, err := v.Digest()
	if err != nil || initial == reordered {
		t.Fatalf("config_order must affect digest: %s %s %v", initial, reordered, err)
	}
	v.Candidates[0].Effort = "changed"
	changed, err := v.Digest()
	if err != nil || changed == reordered {
		t.Fatalf("candidates must also affect digest: %s %s %v", reordered, changed, err)
	}
}

func TestBasePositions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		positions []*int64
		code      string // for a full selection order
		altCode   string // for decision alternatives, which may omit the selected candidate
	}{
		{"empty", []*int64{}, "", ""},
		{"all absent", []*int64{nil, nil, nil}, "", ""},
		{"singleton absent", []*int64{nil}, "", ""},
		{"singleton zero", []*int64{ptr(int64(0))}, "", ""},
		{"identity", []*int64{ptr(int64(0)), ptr(int64(1)), ptr(int64(2))}, "", ""},
		{"permuted", []*int64{ptr(int64(2)), ptr(int64(0)), ptr(int64(1))}, "", ""},
		{"first absent", []*int64{nil, ptr(int64(1)), ptr(int64(2))}, ContractBasePositionInvalid, ContractBasePositionInvalid},
		{"last absent", []*int64{ptr(int64(0)), ptr(int64(1)), nil}, ContractBasePositionInvalid, ContractBasePositionInvalid},
		{"duplicate", []*int64{ptr(int64(0)), ptr(int64(0)), ptr(int64(2))}, ContractBasePositionInvalid, ContractBasePositionInvalid},
		{"nonadjacent duplicate", []*int64{ptr(int64(0)), ptr(int64(1)), ptr(int64(0))}, ContractBasePositionInvalid, ContractBasePositionInvalid},
		{"negative", []*int64{ptr(int64(-1)), ptr(int64(1)), ptr(int64(2))}, ContractBasePositionInvalid, ContractBasePositionInvalid},
		{"gap", []*int64{ptr(int64(0)), ptr(int64(1)), ptr(int64(3))}, ContractBasePositionInvalid, ""},
		{"extreme", []*int64{ptr(int64(math.MaxInt64))}, ContractBasePositionInvalid, ""},
		{"singleton out of range", []*int64{ptr(int64(1))}, ContractBasePositionInvalid, ""},
	} {
		for _, target := range []string{"selection", "alternatives"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				ids := []string{"b", "a", "c"}
				ranked := make([]RankedCandidate, len(tc.positions))
				eligible := make([]ExecutionCandidate, len(tc.positions))
				for i, position := range tc.positions {
					ranked[i] = RankedCandidate{CandidateID: ids[i], Position: int64(i), BasePosition: position, ReasonCodes: []ReasonCode{ReasonConfigOrderPreserved}}
					eligible[i] = ExecutionCandidate{ID: ids[i]}
				}
				if target == "selection" {
					assertCode(t, (SelectionResult{Order: ranked}).ValidateAgainst(eligible), tc.code)
				} else {
					v := fixture[RoutingDecision](t, "decision")
					v.Alternatives = ranked
					assertCode(t, v.Validate(), tc.altCode)
					id, err := v.ContentID()
					assertCode(t, err, tc.altCode)
					if tc.altCode != "" && id != "" {
						t.Fatalf("refused decision has content id %s", id)
					}
				}
			})
		}
	}
}

func TestBasePositionWireAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		position *int64
		field    string
	}{
		{"absent", nil, ""},
		{"zero", ptr(int64(0)), `"base_position":0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := fixture[RoutingDecision](t, "decision")
			v.Alternatives[0].BasePosition = tc.position
			raw, err := canonical.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if tc.field == "" && bytes.Contains(raw, []byte(`"base_position"`)) || tc.field != "" && !bytes.Contains(raw, []byte(tc.field)) {
				t.Fatalf("base_position wire presence: %s", raw)
			}
			id, err := v.ContentID()
			if err != nil {
				t.Fatal(err)
			}
			if tc.position == nil {
				v.Alternatives[0].BasePosition = ptr(int64(0))
			} else {
				v.Alternatives[0].BasePosition = nil
			}
			changed, err := v.ContentID()
			if err != nil || changed == id {
				t.Fatalf("base_position must affect identity: %s %s %v", id, changed, err)
			}
		})
	}
	if ReasonConfigOrderPreserved != "config_order_preserved" {
		t.Fatal("baseline reason wire token changed")
	}
}

func TestConfigOrderCanonicalGoldens(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"candidates", fixture[CandidateSnapshot](t, "candidates")},
		{"decision", fixture[RoutingDecision](t, "decision")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonical.Marshal(tc.value)
			want := readFixture(t, tc.name+".canonical.json")
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("got=%s err=%v want=%s", got, err, want)
			}
		})
	}
}
