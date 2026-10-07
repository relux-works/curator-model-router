package routing

import (
	"encoding/json"
	"errors"
	"flag"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var updateHeadroom = flag.Bool("update", false, "rewrite W1 headroom goldens")

const fixtureDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixtureBundle(n int) DecisionBundle {
	p := DefaultPolicy("v1")
	p.Mode = ModeSelect
	p.Headroom.Enabled = true
	b := DecisionBundle{SchemaVersion: "v1", Envelope: TaskEnvelope{"v1", "Frozen workload", "revision-1", "developer", []string{}, []string{}, []string{}}, Snapshot: CandidateSnapshot{SchemaVersion: "v1", AsOf: 1800000000, ScopeMap: ScopeMap{"v1", []ScopeMapEntry{}}, UsageFacts: []UsageFact{}, Inflight: []Inflight{}, Candidates: []ExecutionCandidate{}, ConfigOrder: []string{}, Source: Provenance{Name: "fixture"}}, Evaluation: EvaluationSnapshot{SchemaVersion: "v1", EvidenceSnapshotDigest: fixtureDigest, Measurements: []string{}, Assessments: []Assessment{}, Estimates: []Estimate{}, EvaluatorVersions: []Provenance{}}, Policy: p, Versions: DecisionOptions{AssessorVersion: Unknown, EstimatorVersion: Unknown, SelectorVersion: SelectorVersion, ApplicabilityScope: "fixture"}}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		key := "key-" + id
		b.Snapshot.ConfigOrder = append(b.Snapshot.ConfigOrder, id)
		b.Snapshot.Candidates = append(b.Snapshot.Candidates, ExecutionCandidate{ID: id, RuntimeBindingID: "binding-" + id, Runtime: "runtime-a", Model: "model-a", Effort: "high", ContextProfile: ContextProfile{"standard", Unknown}, Transport: "native", ProviderID: "provider-a", ToolProfile: "standard", ExecutionMode: "batch", BillingClass: BillingSubscription, UsageKey: &key})
		b.Snapshot.UsageFacts = append(b.Snapshot.UsageFacts, UsageFact{Key: key, Runtime: "runtime-a", State: StateExact, Windows: []UsageWindow{window(b, "session", 300, 2000, 9000)}, RecordDigest: ptrString(fixtureDigest)})
		b.Snapshot.Inflight = append(b.Snapshot.Inflight, Inflight{key, 0})
	}
	return b
}
func ptrString(v string) *string { return &v }
func window(b DecisionBundle, id string, minutes, used, left int64) UsageWindow {
	return UsageWindow{ID: id, Scope: "all", Minutes: minutes, UsedBP: ptr64(used), ResetsAt: ptr64(b.Snapshot.AsOf + left), ObservedAt: ptr64(b.Snapshot.AsOf), Freshness: Fresh}
}

type vector struct {
	name    string
	bundle  DecisionBundle
	order   []string
	codes   map[string][]ReasonCode
	check   func(*testing.T, Explanation)
	refusal string
}

func vectors() []vector {
	b := fixtureBundle(2)
	vs := []vector{}
	add := func(name string, b DecisionBundle, order []string, codes map[string][]ReasonCode, check func(*testing.T, Explanation)) {
		vs = append(vs, vector{name, b, order, codes, check, ""})
	}
	b.Snapshot.UsageFacts[0].Windows = []UsageWindow{window(b, "session", 300, 2000, 16200), window(b, "weekly", 10080, 1000, 60480)}
	b.Snapshot.UsageFacts[1].Windows = []UsageWindow{window(b, "session", 300, 2000, 1800), window(b, "weekly", 10080, 1000, 60480)}
	add("01_expiring_first", b, []string{"b", "a"}, map[string][]ReasonCode{"b": {ReasonQuotaExpiring}}, func(t *testing.T, e Explanation) { requireQuantity(t, e.Candidates[1].Slack, 7000) })
	b = fixtureBundle(2)
	b.Snapshot.UsageFacts[0].Windows = []UsageWindow{window(b, "session", 300, 2000, 1800), window(b, "weekly", 10080, 8000, 544320)}
	b.Snapshot.UsageFacts[1].Windows = []UsageWindow{window(b, "session", 300, 2000, 9000), window(b, "weekly", 10080, 2000, 302400)}
	add("02_weekly_binds", b, []string{"b", "a"}, map[string][]ReasonCode{"a": {ReasonQuotaConserve}}, func(t *testing.T, e Explanation) { requireQuantity(t, e.Candidates[0].Slack, -7000) })
	b = fixtureBundle(6)
	w := b.Snapshot.UsageFacts[0].Windows[0]
	w.Freshness = Stale
	w.UsedBP = ptr64(15000)
	w.ObservedAt = ptr64(b.Snapshot.AsOf - 1000)
	b.Snapshot.UsageFacts[0].Windows[0] = w
	w = b.Snapshot.UsageFacts[1].Windows[0]
	w.Freshness = Expired
	w.UsedBP = ptr64(15000)
	w.ResetsAt = ptr64(b.Snapshot.AsOf)
	b.Snapshot.UsageFacts[1].Windows[0] = w
	w = b.Snapshot.UsageFacts[2].Windows[0]
	w.Freshness = Invalid
	w.Minutes = math.MaxInt64
	w.UsedBP = ptr64(math.MaxInt64)
	b.Snapshot.UsageFacts[2].Windows[0] = w
	w = b.Snapshot.UsageFacts[3].Windows[0]
	w.Freshness = Invalid
	w.ObservedAt = ptr64(b.Snapshot.AsOf + 1)
	b.Snapshot.UsageFacts[3].Windows[0] = w
	w = b.Snapshot.UsageFacts[4].Windows[0]
	w.Freshness = Invalid
	w.ObservedAt = nil
	b.Snapshot.UsageFacts[4].Windows[0] = w
	b.Snapshot.UsageFacts[5].Windows[0] = window(b, "session", 300, 5000, 9000)
	add("03_unknown_neutral", b, []string{"a", "b", "c", "d", "e", "f"}, map[string][]ReasonCode{"a": {ReasonQuotaStale}, "b": {ReasonQuotaExpired}, "c": {ReasonQuotaInvalid}}, func(t *testing.T, e Explanation) {
		for _, x := range e.Candidates[:5] {
			if x.Headroom != nil || x.Band != 0 || x.Reserve || x.Over {
				t.Fatalf("unknown is non-neutral: %+v", x)
			}
		}
	})
	b = fixtureBundle(2)
	w = window(b, "weekly", 10080, 1000, 10000)
	w.Freshness = Stale
	w.ObservedAt = ptr64(b.Snapshot.AsOf - 1000)
	b.Snapshot.UsageFacts[0].Windows = append(b.Snapshot.UsageFacts[0].Windows, w)
	b.Snapshot.UsageFacts[1].Windows[0] = window(b, "session", 300, 5000, 9000)
	add("04_partial_neutral", b, []string{"a", "b"}, map[string][]ReasonCode{"a": {ReasonQuotaPartial}}, func(t *testing.T, e Explanation) {
		if e.Candidates[0].Headroom != nil || e.Candidates[0].Band != 0 {
			t.Fatal("partial earned headroom")
		}
	})
	b = fixtureBundle(2)
	w = window(b, "weekly", 10080, 1000, 10000)
	w.Freshness = Stale
	w.ObservedAt = ptr64(b.Snapshot.AsOf - 3600)
	b.Snapshot.UsageFacts[0].Windows = append(b.Snapshot.UsageFacts[0].Windows, w)
	b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, 0, 1000)
	b.Snapshot.UsageFacts[1].Windows[0] = window(b, "session", 300, 5000, 9000)
	add("05_sparse_push", b, []string{"a", "b"}, map[string][]ReasonCode{"a": {ReasonQuotaPartial, ReasonQuotaStale}}, func(t *testing.T, e Explanation) {
		if e.Candidates[0].Windows[1].Window.ObservedAt == nil || *e.Candidates[0].Windows[1].Window.ObservedAt != 1800000000-3600 {
			t.Fatal("push refreshed untouched window")
		}
	})
	b = fixtureBundle(2)
	b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, 11000, 1000)
	add("06_over_last", b, []string{"b", "a"}, map[string][]ReasonCode{"a": {ReasonQuotaOver}}, nil)
	b = fixtureBundle(2)
	b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, 8500, 1000)
	b.Snapshot.UsageFacts[1].Windows[0] = window(b, "session", 300, 5000, 9000)
	add("07_reserve_developer", b, []string{"b", "a"}, map[string][]ReasonCode{"a": {ReasonQuotaReserve}}, nil)
	b.Envelope.Role = "reviewer"
	// Config order [a,b] keeps this vector discriminating: a is below the
	// reserve, so only the protected role keeps it first.
	b.Snapshot.ConfigOrder = []string{"a", "b"}
	add("07_reserve_reviewer", b, []string{"a", "b"}, nil, func(t *testing.T, e Explanation) {
		for _, c := range e.Candidates {
			if c.Reserve {
				t.Fatalf("reviewer candidate %s reserved", c.CandidateID)
			}
		}
	})
	b = fixtureBundle(2)
	b.Snapshot.Inflight[0].Runs = 3
	b.Snapshot.Inflight[1].Runs = 1
	// Config order [a,b]: b wins only through its lower in-flight count.
	b.Snapshot.ConfigOrder = []string{"a", "b"}
	add("08_inflight_spread", b, []string{"b", "a"}, map[string][]ReasonCode{"a": {ReasonInflightSpread}, "b": {ReasonInflightSpread}}, nil)
	b = fixtureBundle(5)
	b.Policy.Headroom.Equivalence = DeclaredGroups
	b.Policy.Headroom.Groups = [][]EquivalentPair{{{Model: "model-a", Effort: "high"}}}
	b.Snapshot.Candidates[1].BillingClass = BillingMetered
	b.Snapshot.Candidates[1].UsageKey = nil
	b.Snapshot.Candidates[2].Model = "model-outside"
	b.Snapshot.Candidates[3].BillingClass = BillingLocal
	b.Snapshot.Candidates[3].UsageKey = nil
	b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, 11000, 1000)
	add("09_group_slots", b, []string{"e", "b", "c", "d", "a"}, nil, nil)
	b.Policy.Headroom.Groups = append(b.Policy.Headroom.Groups, b.Policy.Headroom.Groups[0])
	vs = append(vs, vector{name: "09_overlap_refusal", bundle: b, refusal: "headroom_groups_overlap"})
	b = fixtureBundle(2)
	b.Snapshot.UsageFacts[0].Windows[0].Scope = "family-a"
	b.Snapshot.UsageFacts[1].Windows[0].Scope = "family-b"
	b.Snapshot.ScopeMap.Entries = []ScopeMapEntry{{"runtime-a", "family-b", []string{"model-a"}}}
	add("10_scopes", b, []string{"b", "a"}, map[string][]ReasonCode{"a": {ReasonQuotaScopeUnmapped, ReasonQuotaNoApplicableWindow}}, nil)
	b = fixtureBundle(2)
	for i := range b.Snapshot.UsageFacts {
		b.Snapshot.UsageFacts[i].Windows[0] = window(b, "session", 300, 9000, 1000)
	}
	b.Snapshot.ConfigOrder = []string{"b", "a"}
	add("11_all_reserved_rank", b, []string{"b", "a"}, map[string][]ReasonCode{"a": {ReasonReserveBreached}}, nil)
	b.Policy.Headroom.OnAllReserved = ReservedAbstain
	add("11_all_reserved_abstain", b, []string{}, nil, func(t *testing.T, e Explanation) {
		if e.Abstention == nil || e.Abstention.Reason != ReasonReserveBreached {
			t.Fatal("missing reserve abstention")
		}
	})
	b = fixtureBundle(2)
	b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, 5001, 9000)
	b.Snapshot.UsageFacts[1].Windows[0] = window(b, "session", 300, 5000, 9000)
	add("12_negative_floor", b, []string{"b", "a"}, nil, func(t *testing.T, e Explanation) {
		requireQuantity(t, e.Candidates[0].Slack, -1)
		if e.Candidates[0].Band != -1 {
			t.Fatal("division did not floor")
		}
	})
	b = fixtureBundle(2)
	b.Snapshot.AsOf = MinTimestamp
	b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", MaxMinutes, MaxUsedBP, MaxTimestamp-MinTimestamp)
	b.Snapshot.UsageFacts[1].Windows[0] = window(b, "session", MinMinutes, MinUsedBP, 1)
	add("12_extreme_bounds", b, []string{"b", "a"}, map[string][]ReasonCode{"a": {ReasonQuotaOver}}, func(t *testing.T, e Explanation) { requireQuantity(t, e.Candidates[0].Slack, -10000) })
	b.Snapshot.AsOf = MaxTimestamp + 1
	vs = append(vs, vector{name: "12_asof_refusal", bundle: b, refusal: "routing_invalid_as_of"})
	return vs
}
func requireQuantity(t *testing.T, v *int64, want int64) {
	t.Helper()
	if v == nil || *v != want {
		t.Fatalf("quantity=%v want=%d", v, want)
	}
}
func encoded(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}
func golden(t *testing.T, path string, raw []byte) {
	t.Helper()
	if *updateHeadroom {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(expected) {
		t.Fatalf("golden differs: %s; rewrite only with -update", path)
	}
}
func TestHeadroomSemanticVectors(t *testing.T) {
	testHeadroomVectors(t, false)
}
func TestHeadroomGoldenVectors(t *testing.T) {
	testHeadroomVectors(t, true)
}
func testHeadroomVectors(t *testing.T, compareGoldens bool) {
	for _, v := range vectors() {
		t.Run(v.name, func(t *testing.T) {
			dir := filepath.Join("..", "..", "testdata", "headroom", v.name)
			raw := encoded(t, v.bundle)
			strategy, err := Strategy(v.bundle.Policy)
			if err == nil {
				_, err = strategy.Select(v.bundle.Snapshot, v.bundle.Evaluation, v.bundle.Policy, v.bundle.Envelope)
			}
			if v.refusal != "" {
				var typed *Error
				if !errors.As(err, &typed) || typed.Code != v.refusal {
					t.Fatalf("refusal=%v want=%s", err, v.refusal)
				}
				if compareGoldens {
					golden(t, filepath.Join(dir, "input.json"), raw)
					golden(t, filepath.Join(dir, "expected.json"), encoded(t, map[string]string{"refusal": v.refusal}))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, err := LoadBundle(raw)
			if err != nil {
				t.Fatal(err)
			}
			r, err := strategy.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			if err != nil {
				t.Fatal(err)
			}
			order := []string{}
			for _, x := range r.Order {
				order = append(order, x.CandidateID)
			}
			if !slices.Equal(order, v.order) {
				t.Fatalf("order=%v want=%v", order, v.order)
			}
			ex, err := Explain(b)
			if err != nil {
				t.Fatal(err)
			}
			for id, codes := range v.codes {
				found := false
				for _, x := range ex.Candidates {
					if x.CandidateID == id {
						found = true
						for _, code := range codes {
							if !slices.Contains(x.ReasonCodes, code) {
								t.Fatalf("%s missing %s: %v", id, code, x.ReasonCodes)
							}
						}
					}
				}
				if !found {
					t.Fatal("missing candidate")
				}
			}
			if v.check != nil {
				v.check(t, ex)
			}
			reasons := map[string][]ReasonCode{}
			for _, x := range ex.Candidates {
				reasons[x.CandidateID] = x.ReasonCodes
			}
			expected := encoded(t, struct {
				Order   []string                `json:"order"`
				Reasons map[string][]ReasonCode `json:"reason_codes"`
				Explain Explanation             `json:"explain"`
			}{order, reasons, ex})
			decision, err := Route(b)
			if err != nil {
				t.Fatal(err)
			}
			if err = decision.Decision.VerifyContentID(); err != nil {
				t.Fatal(err)
			}
			replay, err := Replay(b, decision.Decision)
			if err != nil || !replay.Equal {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
			if compareGoldens {
				golden(t, filepath.Join(dir, "input.json"), raw)
				golden(t, filepath.Join(dir, "expected.json"), expected)
			}
		})
	}
}

func TestConfigOrderParityAndModes(t *testing.T) {
	for _, ids := range [][]string{{}, {"a"}, {"z", "a", "m"}, {"a", "b", "c", "d"}} {
		t.Run(strings.Join(ids, "_"), func(t *testing.T) {
			b := fixtureBundle(len(ids))
			b.Snapshot.ConfigOrder = append([]string{}, ids...)
			for i, id := range ids {
				b.Snapshot.Candidates[i].ID = id
			}
			slices.SortFunc(b.Snapshot.Candidates, func(a, b ExecutionCandidate) int { return strings.Compare(a.ID, b.ID) })
			r, err := (ConfigOrderStrategy{}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, x := range r.Order {
				got = append(got, x.CandidateID)
				if !slices.Contains(x.ReasonCodes, ReasonConfigOrderPreserved) {
					t.Fatal("missing baseline reason")
				}
			}
			if !slices.Equal(got, ids) {
				t.Fatalf("got %v want %v", got, ids)
			}
			b.Policy.Mode = ModeOff
			b.Policy.Strategy = StrategyQualityFirst
			routed, err := Route(b)
			if err != nil || routed.Decision != nil {
				t.Fatalf("off changed choice: %+v %v", routed, err)
			}
			if len(ids) > 0 && routed.EffectiveCandidate.ID != ids[0] {
				t.Fatal("off changed baseline")
			}
		})
	}
	b := vectors()[0].bundle
	b.Policy.Mode = ModeShadow
	r, err := Route(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Decision.SelectedCandidate.ID != "b" || r.EffectiveCandidate.ID != "a" {
		t.Fatalf("shadow: %+v", r)
	}
	b.Policy.Mode = ModeRecommend
	r, err = Route(b)
	if err != nil || r.EffectiveCandidate.ID != "a" {
		t.Fatalf("recommend: %+v %v", r, err)
	}
	b = fixtureBundle(0)
	r, err = Route(b)
	if err != nil || r.Decision.Outcome != OutcomeNoEligibleCandidates || r.EffectiveCandidate != nil {
		t.Fatalf("empty: %+v %v", r, err)
	}
}

type fixedStrategy struct {
	result SelectionResult
	err    error
}

func (f fixedStrategy) Select(CandidateSnapshot, EvaluationSnapshot, RoutingPolicy, TaskEnvelope) (SelectionResult, error) {
	return f.result, f.err
}
func TestBaseAbstentionAndTypedRefusals(t *testing.T) {
	b := fixtureBundle(2)
	r := SelectionResult{Abstention: &Abstention{Reason: ReasonQuotaUnknown}}
	h := HeadroomStrategy{Base: fixedStrategy{result: r}}
	actual, err := h.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil || !reflect.DeepEqual(r, actual) {
		t.Fatalf("base abstention lost: %+v %v", actual, err)
	}
	d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
	if err != nil {
		t.Fatal(err)
	}
	if d.Outcome != OutcomeAbstain {
		t.Fatal("abstain outcome lost")
	}
	for _, floor := range []bool{false, true} {
		fallback, err := BaselineFallback(*d, b.Snapshot, b.Snapshot.Candidates[0], []ReasonCode{ReasonQuotaUnknown}, floor)
		if floor {
			if err == nil {
				t.Fatal("fallback violated floor")
			}
		} else {
			if err != nil || fallback.SelectionOrigin != OriginBaselineAfterAbstain || fallback.Outcome != OutcomeSelected {
				t.Fatalf("fallback=%+v err=%v", fallback, err)
			}
			if err = fallback.VerifyContentID(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []StrategyName{StrategyQualityFirst, StrategyCostWithQualityFloor} {
		b.Policy.Strategy = name
		_, err = Strategy(b.Policy)
		requireErrorCode(t, err, "routing_strategy_not_implemented")
	}
	b.Policy.Strategy = StrategyConfigOrder
	b.Policy.Headroom.Equivalence = FitnessBand
	strategy, err := Strategy(b.Policy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = strategy.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	requireErrorCode(t, err, "headroom_partition_required")
	b.Policy.Headroom.Equivalence = SamePairAnyHome
	malformed := SelectionResult{Order: []RankedCandidate{{CandidateID: "a", Position: 0, ReasonCodes: []ReasonCode{}}}}
	_, err = (HeadroomStrategy{Base: fixedStrategy{result: malformed}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	requireErrorCode(t, err, "routing_invalid_selection")
}
func requireErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error=%v want code=%s", err, code)
	}
}
func TestUnknownStatesAndMissingInputs(t *testing.T) {
	for _, state := range []State{StateAbsent, StateNotSupported, StateUnavailable} {
		t.Run(string(state), func(t *testing.T) {
			b := fixtureBundle(1)
			b.Snapshot.UsageFacts[0].State = state
			if state == StateAbsent {
				b.Snapshot.UsageFacts[0].Windows = []UsageWindow{}
				b.Snapshot.UsageFacts[0].RecordDigest = nil
			}
			ex, err := Explain(b)
			if err != nil {
				t.Fatal(err)
			}
			x := ex.Candidates[0]
			if x.Headroom != nil || x.Slack != nil || x.Over || x.Reserve || x.Band != 0 || !slices.Contains(x.ReasonCodes, ReasonQuotaUnknown) {
				t.Fatalf("unreadable known: %+v", x)
			}
		})
	}
	b := fixtureBundle(1)
	b.Snapshot.UsageFacts = []UsageFact{}
	b.Snapshot.Inflight = []Inflight{}
	ex, err := Explain(b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ex.Candidates[0].ReasonCodes, ReasonInflightUnknown) || !slices.Contains(ex.Candidates[0].ReasonCodes, ReasonQuotaUnknown) {
		t.Fatal("missing unknown reasons")
	}
	b = fixtureBundle(1)
	b.Snapshot.UsageFacts[0].Windows[0].ResetsAt = nil
	ex, err = Explain(b)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Candidates[0].Headroom == nil || ex.Candidates[0].Slack != nil || ex.Candidates[0].Band != 0 {
		t.Fatal("absent reset semantics")
	}
	b.Policy.Headroom.Windows = WindowFilter{IDs: []string{}}
	ex, err = Explain(b)
	if err != nil || ex.Candidates[0].Headroom != nil {
		t.Fatalf("empty filter: %+v %v", ex, err)
	}
}
func TestReplayMemberDifferences(t *testing.T) {
	b := fixtureBundle(2)
	b.Evaluation.Measurements = []string{"obs:" + strings.Repeat("b", 64)}
	b.Evaluation.Estimates = []Estimate{{CandidateID: "a", ContributingRecordIDs: []string{"note:" + strings.Repeat("c", 64)}, EstimatorVersion: "v1"}}
	r, err := Route(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Decision.EvidenceRefs) != 2 {
		t.Fatal("missing evidence refs")
	}
	ex, err := ExplainDecision(b, *r.Decision)
	if err != nil || !strings.Contains(ex.RenderHuman(), "freshness=fresh") {
		t.Fatalf("explain=%+v %v", ex, err)
	}
	changed := *r.Decision
	candidate := *changed.SelectedCandidate
	candidate.Model = "model-changed"
	changed.SelectedCandidate = &candidate
	changed.PreparedAt++
	changed.DecisionID = "sha256:" + strings.Repeat("d", 64)
	replay, err := Replay(b, &changed)
	if err != nil || replay.Equal {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	members := []string{}
	for _, d := range replay.Differences {
		members = append(members, d.Member)
	}
	want := []string{"$.decision_id", "$.prepared_at", "$.selected_candidate.model"}
	if !slices.Equal(members, want) {
		t.Fatalf("members=%v want=%v", members, want)
	}
	changed = *r.Decision
	changed.ExpiresAt = ptr64(b.Snapshot.AsOf + 1)
	replay, err = Replay(b, &changed)
	if err != nil || len(replay.Differences) != 1 || replay.Differences[0].Member != "$.expires_at" {
		t.Fatalf("optional diff: %+v %v", replay, err)
	}
	b.Envelope.SubstantiveRevision = "changed"
	_, err = ExplainDecision(b, *r.Decision)
	requireErrorCode(t, err, "routing_decision_input_mismatch")
	b.Versions.SelectorVersion = "future"
	_, err = Route(b)
	requireErrorCode(t, err, "routing_selector_version_mismatch")
}
func TestInputStrictnessAndCreditsDisplay(t *testing.T) {
	b := fixtureBundle(2)
	balance := 12.5
	b.Snapshot.UsageFacts[0].Credits = &Credits{Balance: &balance, Unit: "credits"}
	b.Snapshot.UsageFacts[0].FailureCount = 10
	raw := encoded(t, b)
	loaded, err := LoadBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := Explain(loaded)
	if err != nil || !strings.Contains(ex.RenderHuman(), "display only") {
		t.Fatalf("credits %v", err)
	}
	before, err := Route(b)
	if err != nil {
		t.Fatal(err)
	}
	b.Snapshot.UsageFacts[0].Credits = nil
	b.Snapshot.UsageFacts[0].FailureCount = 0
	after, err := Route(b)
	if err != nil || before.Decision.SelectedCandidate.ID != after.Decision.SelectedCandidate.ID {
		t.Fatal("credits/failures changed ordering")
	}
	for _, raw := range []string{`{"schema_version":"v1","extra":1}`, `{"schema_version":"v1","Envelope":{}}`, `{"schema_version":"v1","policy":null}`, `{"schema_version":"v1","schema_version":"v1"}`} {
		if _, err := LoadBundle([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func FuzzHeadroomPermutation(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7})
	f.Add([]byte{255, 255, 255, 255})
	f.Add([]byte{10, 0, 9, 1, 8, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		for _, mode := range []Equivalence{DeclaredGroups, SamePairAnyHome} {
			n := 1 + int(data[0]%16)
			b := fixtureBundle(n)
			b.Policy.Headroom.Equivalence = mode
			if mode == DeclaredGroups {
				b.Policy.Headroom.Groups = [][]EquivalentPair{{{Model: "model-a", Effort: "high"}, {Model: "model-a", Effort: "low"}}}
			}
			for i := 0; i < n; i++ {
				v := int64(data[i%len(data)])
				profile := data[(i+1)%len(data)]
				c := &b.Snapshot.Candidates[i]
				if profile%2 == 1 {
					c.Effort = "low"
				}
				if profile%3 == 1 {
					c.ToolProfile = "alternate-tools"
				}
				if profile%3 == 2 {
					c.ContextProfile.Name = "alternate-context"
				}
				b.Snapshot.UsageFacts[i].Windows[0] = window(b, "session", 1+v*2000, v*3900, 1+v*10)
				b.Snapshot.Inflight[i].Runs = v
				switch v % 5 {
				case 0:
					c.BillingClass = BillingLocal
					c.UsageKey = nil
				case 1:
					c.BillingClass = BillingMetered
					c.UsageKey = nil
				case 2:
					c.Model = "outside"
				case 3:
					b.Snapshot.UsageFacts[i].Windows[0].Freshness = Invalid
					b.Snapshot.UsageFacts[i].Windows[0].Minutes = math.MinInt64
					b.Snapshot.UsageFacts[i].Windows[0].UsedBP = ptr64(math.MaxInt64)
				}
			}
			base := fixedOrder(b, reversedIDs(b))
			h := HeadroomStrategy{Base: fixedStrategy{result: base}}
			raw := encoded(t, b)
			r, err := h.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.ValidateAgainst(b.Snapshot.Candidates); err != nil {
				t.Fatal(err)
			}
			if string(raw) != string(encoded(t, b)) {
				t.Fatal("selector mutated inputs")
			}
			want := referenceHeadroomOrder(b, base)
			if got := rankedIDs(r); !slices.Equal(got, want) {
				t.Fatalf("mode=%s order=%v want=%v", mode, got, want)
			}
			for _, ranked := range base.Order {
				c := candidateByID(t, b, ranked.CandidateID)
				if c.BillingClass != BillingSubscription || (mode == DeclaredGroups && c.Model == "outside") {
					if r.Order[ranked.Position].CandidateID != c.ID {
						t.Fatal("fixed slot moved")
					}
				}
			}
			for _, ranked := range r.Order {
				requireQuantity(t, ranked.BasePosition, int64(slices.Index(rankedIDs(base), ranked.CandidateID)))
			}
			// ConfigOrder is semantic input to config-order, but a fixed base owns its order.
			slices.Reverse(b.Snapshot.ConfigOrder)
			again, err := h.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			if err != nil || !reflect.DeepEqual(r, again) {
				t.Fatalf("mode=%s non-deterministic or config order leaked into fixed base: %v", mode, err)
			}
		}
	})
}
func TestMapOrderAndBoundaryRounding(t *testing.T) {
	b := fixtureBundle(2)
	b.Evaluation.Assessments = []Assessment{{ID: "a", Requirements: Requirements{Items: []Requirement{}}, Features: map[string]string{"a": "one", "z": "two"}, AssessorVersion: "v1"}}
	r, err := Route(b)
	if err != nil {
		t.Fatal(err)
	}
	b.Evaluation.Assessments[0].Features = map[string]string{}
	b.Evaluation.Assessments[0].Features["z"] = "two"
	b.Evaluation.Assessments[0].Features["a"] = "one"
	again, err := Route(b)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("map order changed decision")
	}
	for _, tc := range []struct {
		percent string
		want    int64
	}{{"1.005", 100}, {"1.015", 102}, {"9999.995", 1000000}} {
		got, err := PercentToBP(tc.percent)
		if err != nil || got != tc.want {
			t.Fatalf("%s=%d %v", tc.percent, got, err)
		}
	}
	b.Snapshot.AsOf = MaxTimestamp
	b.Snapshot.UsageFacts = []UsageFact{}
	if _, err = Route(b); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedBaselineFallbackReplay(t *testing.T) {
	b := fixtureBundle(2)
	for i := range b.Snapshot.UsageFacts {
		b.Snapshot.UsageFacts[i].Windows[0] = window(b, "session", 300, 9000, 1000)
	}
	b.Policy.Headroom.OnAllReserved = ReservedAbstain
	b.Versions.BaselineFallbackReasons = []ReasonCode{ReasonReserveBreached}
	routed, err := Route(b)
	if err != nil {
		t.Fatal(err)
	}
	if routed.Decision.SelectionOrigin != OriginBaselineAfterAbstain || routed.EffectiveCandidate.ID != "a" {
		t.Fatalf("fallback: %+v", routed)
	}
	replay, err := Replay(b, routed.Decision)
	if err != nil || !replay.Equal {
		t.Fatalf("fallback replay: %+v %v", replay, err)
	}
	ex, err := ExplainDecision(b, *routed.Decision)
	if err != nil || ex.SelectedCandidateID != "a" || ex.SelectionOrigin != OriginBaselineAfterAbstain || ex.Candidates[0].FinalPosition == nil {
		t.Fatalf("fallback explanation: %+v %v", ex, err)
	}
	foreign := b.Snapshot.Candidates[0]
	foreign.Model = "foreign"
	abstained, err := (HeadroomStrategy{Base: ConfigOrderStrategy{}}).Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, abstained, b.Versions)
	if err != nil {
		t.Fatal(err)
	}
	_, err = BaselineFallback(*decision, b.Snapshot, foreign, []ReasonCode{ReasonReserveBreached}, false)
	requireErrorCode(t, err, "routing_fallback_not_allowed")
	_, err = BaselineFallback(*decision, b.Snapshot, b.Snapshot.Candidates[0], []ReasonCode{}, false)
	requireErrorCode(t, err, "routing_fallback_not_allowed")
	b.Versions.MandatoryEvidenceFloor = true
	_, err = Route(b)
	requireErrorCode(t, err, "routing_fallback_not_allowed")
}

func TestPartialOverAndExecutionGroupIsolation(t *testing.T) {
	b := fixtureBundle(2)
	b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, 12000, 1000)
	stale := window(b, "weekly", 10080, 0, 10000)
	stale.Freshness = Stale
	stale.ObservedAt = ptr64(b.Snapshot.AsOf - 1000)
	b.Snapshot.UsageFacts[0].Windows = append(b.Snapshot.UsageFacts[0].Windows, stale)
	ex, err := Explain(b)
	if err != nil {
		t.Fatal(err)
	}
	if !ex.Candidates[0].Over || ex.Candidates[0].Headroom != nil || ex.Candidates[0].Reserve || ex.Candidates[0].Band != 0 || *ex.Candidates[0].FinalPosition != 1 {
		t.Fatalf("partial known over: %+v", ex.Candidates[0])
	}
	b.Snapshot.Candidates[1].ToolProfile = "different-tools"
	ex, err = Explain(b)
	if err != nil {
		t.Fatal(err)
	}
	if *ex.Candidates[0].FinalPosition != 0 || *ex.Candidates[1].FinalPosition != 1 || ex.Candidates[0].Group == ex.Candidates[1].Group {
		t.Fatal("different execution profiles joined a home group")
	}
}
