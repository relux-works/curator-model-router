package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func rankedIDs(r SelectionResult) []string {
	ids := []string{}
	for _, c := range r.Order {
		ids = append(ids, c.CandidateID)
	}
	return ids
}
func reversedIDs(b DecisionBundle) []string {
	ids := slices.Clone(b.Snapshot.ConfigOrder)
	slices.Reverse(ids)
	return ids
}
func fixedOrder(b DecisionBundle, ids []string) SelectionResult {
	r := SelectionResult{Order: []RankedCandidate{}}
	for i, id := range ids {
		r.Order = append(r.Order, RankedCandidate{CandidateID: id, Position: int64(i), ReasonCodes: []ReasonCode{ReasonConfigOrderPreserved}})
	}
	return r
}
func candidateByID(t *testing.T, b DecisionBundle, id string) ExecutionCandidate {
	t.Helper()
	for _, c := range b.Snapshot.Candidates {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("missing candidate %s", id)
	return ExecutionCandidate{}
}
func TestHeadroomSortPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		ids, want []string
		setup     func(*DecisionBundle)
	}{
		{"over_beats_reserve", []string{"b", "a"}, []string{"b", "a"}, func(b *DecisionBundle) {
			b.Snapshot.UsageFacts[0].Windows[0] = window(*b, "session", 300, 10000, 1)
			w := window(*b, "weekly", 10080, 0, 1000)
			w.Freshness = Invalid
			b.Snapshot.UsageFacts[0].Windows = append(b.Snapshot.UsageFacts[0].Windows, w)
			b.Snapshot.UsageFacts[1].Windows[0] = window(*b, "session", 300, 9500, 16200)
		}},
		{"band_beats_inflight", []string{"b", "a"}, []string{"a", "b"}, func(b *DecisionBundle) {
			b.Snapshot.UsageFacts[0].Windows[0] = window(*b, "session", 300, 2000, 1800)
			b.Snapshot.Inflight[0].Runs = 99
		}},
		{"all_equal_preserves_base", []string{"c", "a", "b"}, []string{"c", "a", "b"}, func(*DecisionBundle) {}},
		{"reserve_beats_band", []string{"b", "a"}, []string{"b", "a"}, func(b *DecisionBundle) {
			b.Snapshot.UsageFacts[0].Windows[0] = window(*b, "session", 300, 8500, 1)
			b.Snapshot.UsageFacts[1].Windows[0] = window(*b, "session", 300, 5000, 16200)
		}},
		{"inflight_beats_base_position", []string{"b", "a"}, []string{"a", "b"}, func(b *DecisionBundle) { b.Snapshot.Inflight[1].Runs = 99 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := fixtureBundle(len(tc.ids))
			tc.setup(&b)
			base := fixedOrder(b, tc.ids)
			h := HeadroomStrategy{Base: fixedStrategy{result: base}}
			got, ex, err := h.evaluate(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(rankedIDs(got), tc.want) {
				t.Fatalf("got=%v want=%v quantities=%+v", rankedIDs(got), tc.want, ex.Candidates)
			}
			// Confirm that each opposing lower-priority key is actually in play.
			x, y := ex.Candidates[0], ex.Candidates[1]
			switch tc.name {
			case "over_beats_reserve":
				if x.Over || !x.Reserve || !y.Over || y.Reserve || y.Band <= x.Band {
					t.Fatalf("invalid precedence fixture: %+v", ex)
				}
			case "band_beats_inflight":
				if y.Band <= x.Band || y.Inflight <= x.Inflight || x.Over != y.Over || x.Reserve != y.Reserve {
					t.Fatal("invalid precedence fixture")
				}
			case "reserve_beats_band":
				if x.Reserve || !y.Reserve || y.Band <= x.Band || x.Over != y.Over {
					t.Fatal("invalid precedence fixture")
				}
			case "inflight_beats_base_position":
				if y.Inflight >= x.Inflight || x.Band != y.Band || x.Reserve != y.Reserve || x.Over != y.Over {
					t.Fatal("invalid precedence fixture")
				}
			}
			for _, r := range got.Order {
				requireQuantity(t, r.BasePosition, int64(slices.Index(tc.ids, r.CandidateID)))
			}
			if err = got.ValidateAgainst(b.Snapshot.Candidates); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHeadroomCandidateIDSafetyNet(t *testing.T) {
	// Equal base positions cannot occur in a validated selection. Exercise the
	// defensive comparator directly so its lexical safety net remains correct.
	a := CandidateExplanation{CandidateID: "a", BasePosition: 0}
	b := CandidateExplanation{CandidateID: "b", BasePosition: 0}
	if compareHeadroom(a, b) >= 0 || compareHeadroom(b, a) <= 0 || compareHeadroom(a, a) != 0 {
		t.Fatal("candidate-id safety net must be lexical ascending")
	}
}

func TestHeadroomOverBoundary(t *testing.T) {
	for _, tc := range []struct {
		used, headroom int64
		over           bool
	}{{10000, 0, true}, {9999, 1, false}} {
		b := fixtureBundle(1)
		b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, tc.used, 1)
		ex, err := Explain(b)
		if err != nil {
			t.Fatal(err)
		}
		x := ex.Candidates[0]
		requireQuantity(t, x.Headroom, tc.headroom)
		if x.Over != tc.over || slices.Contains(x.ReasonCodes, ReasonQuotaOver) != tc.over {
			t.Fatalf("used=%d: %+v", tc.used, x)
		}
	}
}
func TestHeadroomPaceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		width, expiring, conserve, used, left, band int64
		reason                                      ReasonCode
	}{
		{"default_conserve_exact", 1000, 3, -2, 7000, 9000, -2, ReasonQuotaConserve},
		{"default_expiring_exact", 1000, 3, -2, 2000, 9000, 3, ReasonQuotaExpiring},
		{"custom_conserve_exact", 500, 4, -3, 6500, 9000, -3, ReasonQuotaConserve},
		{"custom_expiring_exact", 500, 4, -3, 3000, 9000, 4, ReasonQuotaExpiring},
		{"on_pace", 1000, 3, -2, 5000, 9000, 0, ReasonQuotaOnPace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fixtureBundle(1)
			b.Policy.Headroom.BandWidthBP = tc.width
			b.Policy.Headroom.ExpiringBand = tc.expiring
			b.Policy.Headroom.ConserveBand = tc.conserve
			b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, tc.used, tc.left)
			ex, err := Explain(b)
			if err != nil {
				t.Fatal(err)
			}
			x := ex.Candidates[0]
			if x.Slack == nil || x.Band != tc.band {
				t.Fatalf("wrong measured band: %+v", x)
			}
			for _, code := range []ReasonCode{ReasonQuotaExpiring, ReasonQuotaOnPace, ReasonQuotaConserve} {
				if slices.Contains(x.ReasonCodes, code) != (code == tc.reason) {
					t.Fatalf("wrong pace reasons: %+v", x)
				}
			}
		})
	}
}
func TestIncompleteFactsHaveNoPaceReason(t *testing.T) {
	for _, thresholds := range [][2]int64{{0, -2}, {3, 0}, {3, -2}} {
		for _, state := range []State{StateUnavailable, StateNotSupported, StateAbsent, StateExact} {
			b := fixtureBundle(1)
			b.Policy.Headroom.ExpiringBand = thresholds[0]
			b.Policy.Headroom.ConserveBand = thresholds[1]
			b.Snapshot.UsageFacts[0].State = state
			if state == StateAbsent {
				b.Snapshot.UsageFacts[0].Windows = []UsageWindow{}
				b.Snapshot.UsageFacts[0].RecordDigest = nil
			}
			if state == StateExact {
				b.Snapshot.UsageFacts[0].Windows[0].ResetsAt = nil
			}
			ex, err := Explain(b)
			if err != nil {
				t.Fatal(err)
			}
			x := ex.Candidates[0]
			if x.Slack != nil || x.Band != 0 {
				t.Fatalf("incomplete fact gained slack: %+v", x)
			}
			for _, code := range []ReasonCode{ReasonQuotaExpiring, ReasonQuotaOnPace, ReasonQuotaConserve} {
				if slices.Contains(x.ReasonCodes, code) {
					t.Fatalf("state=%s thresholds=%v has misleading %s", state, thresholds, code)
				}
			}
			if state == StateUnavailable && !slices.Equal(x.ReasonCodes, []ReasonCode{ReasonConfigOrderPreserved, ReasonQuotaUnknown}) {
				t.Fatalf("unavailable reasons=%v", x.ReasonCodes)
			}
		}
	}
}
func TestModelScopeApplicability(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		entries              []ScopeMapEntry
		unmapped, applicable bool
	}{
		{"missing_entry", []ScopeMapEntry{}, true, false},
		{"other_runtime", []ScopeMapEntry{{"runtime-other", "family", []string{"model-a"}}}, true, false},
		{"mapped_to_other_model", []ScopeMapEntry{{"runtime-a", "family", []string{"model-other"}}}, false, false},
		{"mapped_to_candidate", []ScopeMapEntry{{"runtime-a", "family", []string{"model-a"}}}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fixtureBundle(1)
			b.Snapshot.UsageFacts[0].Windows[0].Scope = "family"
			b.Snapshot.ScopeMap.Entries = tc.entries
			ex, err := Explain(b)
			if err != nil {
				t.Fatal(err)
			}
			x := ex.Candidates[0]
			if x.Windows[0].Applicable != tc.applicable || x.Windows[0].Known != tc.applicable || slices.Contains(x.ReasonCodes, ReasonQuotaScopeUnmapped) != tc.unmapped {
				t.Fatalf("scope semantics: %+v", x)
			}
			if !tc.applicable && (x.Headroom != nil || x.Slack != nil || !slices.Contains(x.ReasonCodes, ReasonQuotaNoApplicableWindow)) {
				t.Fatalf("inapplicable window contributed: %+v", x)
			}
		})
	}
}
func TestFitnessBandRefusesBeforeBase(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		base SelectionResult
	}{
		{"empty", 0, SelectionResult{Order: []RankedCandidate{}}},
		{"base_abstention", 1, SelectionResult{Abstention: &Abstention{Reason: ReasonQuotaUnknown}}},
		{"nonempty", 1, fixedOrder(fixtureBundle(1), []string{"a"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fixtureBundle(tc.n)
			b.Policy.Headroom.Equivalence = FitnessBand
			h := HeadroomStrategy{Base: fixedStrategy{result: tc.base}}
			_, err := h.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
			requireErrorCode(t, err, "headroom_partition_required")
		})
	}
}
func TestC1ConfigOrderDecisionsAndExplanation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		b := fixtureBundle(3)
		b.Policy.Headroom.Enabled = enabled
		b.Snapshot.ConfigOrder = []string{"c", "a", "b"}
		routed, err := Route(b)
		if err != nil {
			t.Fatal(err)
		}
		if routed.Decision.SelectedCandidate.ID != "c" {
			t.Fatal("selected storage order")
		}
		for i, a := range routed.Decision.Alternatives {
			if a.CandidateID != []string{"a", "b"}[i] || a.Position != int64(i+1) {
				t.Fatalf("alternatives=%+v", routed.Decision.Alternatives)
			}
			if enabled {
				requireQuantity(t, a.BasePosition, int64(i+1))
			} else if a.BasePosition != nil {
				t.Fatal("unwrapped result has base_position")
			}
		}
		ex, err := Explain(b)
		if err != nil {
			t.Fatal(err)
		}
		for i, x := range ex.Candidates {
			if x.CandidateID != b.Snapshot.ConfigOrder[i] || x.BasePosition != int64(i) || x.FinalPosition == nil || *x.FinalPosition != int64(i) {
				t.Fatalf("explain=%+v", ex)
			}
		}
		replay, err := Replay(b, routed.Decision)
		if err != nil || !replay.Equal {
			t.Fatalf("replay=%+v err=%v", replay, err)
		}
	}
	for _, mode := range []Mode{ModeOff, ModeShadow, ModeRecommend} {
		b := fixtureBundle(2)
		b.Policy.Mode = mode
		b.Snapshot.ConfigOrder = []string{"b", "a"}
		b.Snapshot.UsageFacts[0].Windows[0] = window(b, "session", 300, 2000, 1800)
		routed, err := Route(b)
		if err != nil {
			t.Fatal(err)
		}
		if routed.EffectiveCandidate == nil || routed.EffectiveCandidate.ID != "b" {
			t.Fatalf("mode=%s changed configured baseline: %+v", mode, routed)
		}
		if mode != ModeOff && (routed.Decision == nil || routed.Decision.SelectedCandidate.ID != "a") {
			t.Fatalf("mode=%s lost recommendation: %+v", mode, routed)
		}
	}

	b := fixtureBundle(3)
	h := HeadroomStrategy{Base: fixedStrategy{result: fixedOrder(b, []string{"c", "a", "b"})}}
	b.Snapshot.UsageFacts[2].Windows[0] = window(b, "session", 300, 10000, 1)
	r, err := h.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
	if err != nil {
		t.Fatal(err)
	}
	if d.SelectedCandidate.ID != "a" || len(d.Alternatives) != 2 || d.Alternatives[0].CandidateID != "b" || d.Alternatives[1].CandidateID != "c" {
		t.Fatalf("decision=%+v", d)
	}
	requireQuantity(t, d.Alternatives[0].BasePosition, 2)
	requireQuantity(t, d.Alternatives[1].BasePosition, 0)
	if err = d.VerifyContentID(); err != nil {
		t.Fatal(err)
	}
	b = fixtureBundle(2)
	b.Snapshot.ConfigOrder = []string{"b", "a"}
	b.Snapshot.AsOf = 0
	b.Policy.Mode = ModeOff
	if err = b.Snapshot.Validate(); err == nil {
		t.Fatal("off regression fixture must be invalid")
	}
	routed, err := Route(b)
	if err != nil || routed.Decision != nil || routed.EffectiveCandidate == nil || routed.EffectiveCandidate.ID != "b" {
		t.Fatalf("off failed to preserve configured baseline: %+v %v", routed, err)
	}
}
func TestSamePairGroupDigest(t *testing.T) {
	b := fixtureBundle(2)
	profile := b.Snapshot.Candidates[0]
	profile.ID = ""
	profile.RuntimeBindingID = ""
	profile.UsageKey = nil
	profile.BillingClass = ""
	raw, err := canonical.Marshal(struct {
		SchemaVersion string             `json:"schema_version"`
		Candidate     ExecutionCandidate `json:"candidate"`
	}{"v1", profile})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	want := "same-pair:" + hex.EncodeToString(digest[:])[:12]
	ex, err := Explain(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ex.Candidates {
		if x.Group != want || len(x.Group) != len("same-pair:")+12 {
			t.Fatalf("group=%s want=%s", x.Group, want)
		}
	}
	for _, field := range []string{"effort", "tools", "context"} {
		changed := fixtureBundle(2)
		switch field {
		case "effort":
			changed.Snapshot.Candidates[1].Effort = "low"
		case "tools":
			changed.Snapshot.Candidates[1].ToolProfile = "other"
		case "context":
			changed.Snapshot.Candidates[1].ContextProfile.Name = "other"
		}
		ex, err = Explain(changed)
		if err != nil {
			t.Fatal(err)
		}
		if ex.Candidates[0].Group == ex.Candidates[1].Group {
			t.Fatalf("%s failed to isolate groups", field)
		}
	}
}

// referenceHeadroomOrder independently evaluates the generated single-window
// facts and the §5.3 key. It uses neither quantities, floorDiv, groupFor nor the
// production comparator. Group slots are collected from the fixed base order.
func referenceHeadroomOrder(b DecisionBundle, base SelectionResult) []string {
	byID := map[string]ExecutionCandidate{}
	for _, c := range b.Snapshot.Candidates {
		byID[c.ID] = c
	}
	key := func(pos int) [5]int64 {
		c := byID[base.Order[pos].CandidateID]
		k := [5]int64{0, 0, 0, 0, int64(pos)}
		for _, count := range b.Snapshot.Inflight {
			if c.UsageKey != nil && count.Key == *c.UsageKey {
				k[3] = count.Runs
			}
		}
		for _, fact := range b.Snapshot.UsageFacts {
			if c.UsageKey == nil || fact.Key != *c.UsageKey || fact.Runtime != c.Runtime {
				continue
			}
			w := fact.Windows[0]
			if w.Freshness != Fresh {
				break
			}
			remaining := int64(10000) - *w.UsedBP
			if remaining < 0 {
				remaining = 0
			}
			if *w.UsedBP >= 10000 {
				k[0] = 1
			}
			if remaining < b.Policy.Headroom.ReserveBP && !slices.Contains(b.Policy.Headroom.ProtectedRoles, b.Envelope.Role) {
				k[1] = 1
			}
			duration := w.Minutes * 60
			left := *w.ResetsAt - b.Snapshot.AsOf
			if left > duration {
				left = duration
			}
			slack := remaining - left*10000/duration
			band := slack / b.Policy.Headroom.BandWidthBP
			if slack < 0 && slack%b.Policy.Headroom.BandWidthBP != 0 {
				band--
			}
			k[2] = -band
		}
		return k
	}
	sameGroup := func(a, c ExecutionCandidate) bool {
		if b.Policy.Headroom.Equivalence == DeclaredGroups {
			return a.Model == "model-a" && c.Model == "model-a"
		}
		a.ID = ""
		c.ID = ""
		a.RuntimeBindingID = ""
		c.RuntimeBindingID = ""
		a.UsageKey = nil
		c.UsageKey = nil
		a.BillingClass = ""
		c.BillingClass = ""
		return reflect.DeepEqual(a, c)
	}
	out := rankedIDs(base)
	visited := make([]bool, len(out))
	for i, ranked := range base.Order {
		c := byID[ranked.CandidateID]
		if visited[i] || c.BillingClass != BillingSubscription || (b.Policy.Headroom.Equivalence == DeclaredGroups && c.Model != "model-a") {
			continue
		}
		slots := []int{}
		for j, other := range base.Order {
			if byID[other.CandidateID].BillingClass == BillingSubscription && sameGroup(c, byID[other.CandidateID]) {
				slots = append(slots, j)
				visited[j] = true
			}
		}
		members := slices.Clone(slots)
		sort.Slice(members, func(i, j int) bool {
			a, c := key(members[i]), key(members[j])
			for n := 0; n < len(a); n++ {
				if a[n] != c[n] {
					return a[n] < c[n]
				}
			}
			return strings.Compare(base.Order[members[i]].CandidateID, base.Order[members[j]].CandidateID) < 0
		})
		for j, slot := range slots {
			out[slot] = base.Order[members[j]].CandidateID
		}
	}
	return out
}

func TestHeadroomEvaluatePassesBaseAbstention(t *testing.T) {
	b := fixtureBundle(2)
	h := HeadroomStrategy{Base: fixedStrategy{result: SelectionResult{Abstention: &Abstention{Reason: ReasonQuotaUnknown}}}}
	r, ex, err := h.evaluate(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	if r.Abstention == nil || r.Abstention.Reason != ReasonQuotaUnknown || len(r.Order) != 0 {
		t.Fatalf("base abstention not passed through: %+v", r)
	}
	if ex.Abstention == nil || ex.Abstention.Reason != ReasonQuotaUnknown {
		t.Fatalf("explanation lost base abstention: %+v", ex.Abstention)
	}
}
