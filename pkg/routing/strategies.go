package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

// ConfigOrderStrategy preserves the caller's declared order, including empty sets.
type ConfigOrderStrategy struct{}

func (ConfigOrderStrategy) Select(s CandidateSnapshot, _ EvaluationSnapshot, _ RoutingPolicy, _ TaskEnvelope) (SelectionResult, error) {
	if err := s.Validate(); err != nil {
		return SelectionResult{}, err
	}
	r := SelectionResult{Order: make([]RankedCandidate, len(s.ConfigOrder))}
	for i, id := range s.ConfigOrder {
		r.Order[i] = RankedCandidate{CandidateID: id, Position: int64(i), ReasonCodes: []ReasonCode{ReasonConfigOrderPreserved}}
	}
	return r, r.ValidateAgainst(s.Candidates)
}

// HeadroomStrategy wraps any pure base strategy. Its immutable group slots are
// taken from the base order, never the storage order of the snapshot.
type HeadroomStrategy struct{ Base SelectionStrategy }

// qualityHeadroomStrategy runs the base before partition requirements and guards
// every movable subscription subgroup before reusing the historical slot logic.
type qualityHeadroomStrategy struct{ Base SelectionStrategy }

func (qualityHeadroomStrategy) selectorVersion() string { return QualitySelectorVersion }

// Preserve a versioned base's validation order through public wrapper nesting.
func (h HeadroomStrategy) selectorVersion() string {
	if base, ok := h.Base.(interface{ selectorVersion() string }); ok {
		return base.selectorVersion()
	}
	return SelectorVersion
}

func (h qualityHeadroomStrategy) Select(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, t TaskEnvelope) (SelectionResult, error) {
	r, _, err := (HeadroomStrategy{Base: h.Base}).evaluateVersion(s, e, p, t, true)
	return r, err
}

func (h HeadroomStrategy) Select(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, t TaskEnvelope) (SelectionResult, error) {
	r, _, err := h.evaluate(s, e, p, t)
	return r, err
}
func Strategy(p RoutingPolicy) (SelectionStrategy, error) {
	if err := validateQualitySelectorInput(nil, nil, p, SelectorVersion, nil); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Strategy != StrategyConfigOrder {
		return nil, fail("routing_strategy_not_implemented", "base strategy requires slice B")
	}
	var base SelectionStrategy = ConfigOrderStrategy{}
	if p.Headroom.Enabled {
		base = HeadroomStrategy{Base: base}
	}
	return base, nil
}
func addReason(r []ReasonCode, c ReasonCode) []ReasonCode {
	r = append(r, c)
	slices.Sort(r)
	return slices.Compact(r)
}
func floorDiv(n, d int64) int64 {
	q := n / d
	if n%d < 0 {
		q--
	}
	return q
}

// WindowExplanation retains even ignored and unknown measurements for display.
type WindowExplanation struct {
	Window     UsageWindow `json:"window"`
	Applicable bool        `json:"applicable"`
	Known      bool        `json:"known"`
	Remaining  *int64      `json:"remaining_bp,omitempty"`
	TimeLeft   *int64      `json:"time_left_bp,omitempty"`
	Slack      *int64      `json:"slack_bp,omitempty"`
}
type CandidateExplanation struct {
	CandidateID   string              `json:"candidate_id"`
	BillingClass  BillingClass        `json:"billing_class"`
	BasePosition  int64               `json:"base_position"`
	Group         string              `json:"group,omitempty"`
	Windows       []WindowExplanation `json:"windows"`
	Headroom      *int64              `json:"headroom_bp,omitempty"`
	Slack         *int64              `json:"slack_bp,omitempty"`
	Band          int64               `json:"band"`
	Over          bool                `json:"over"`
	Reserve       bool                `json:"reserve"`
	Inflight      int64               `json:"inflight"`
	ReasonCodes   []ReasonCode        `json:"reason_codes"`
	Credits       *Credits            `json:"credits,omitempty"`
	FinalPosition *int64              `json:"final_position,omitempty"`
	Estimate      *Estimate           `json:"estimate,omitempty"`
	Quality       *QualityExplanation `json:"quality,omitempty"`
	Cost          *CostExplanation    `json:"cost,omitempty"`
}
type Explanation struct {
	Outcome             OutcomeKind            `json:"outcome,omitempty"`
	SelectionOrigin     SelectionOrigin        `json:"selection_origin,omitempty"`
	SelectedCandidateID string                 `json:"selected_candidate_id,omitempty"`
	SchemaVersion       string                 `json:"schema_version"`
	AsOf                int64                  `json:"as_of"`
	Candidates          []CandidateExplanation `json:"candidates"`
	Abstention          *Abstention            `json:"abstention,omitempty"`
}

func ptr64(v int64) *int64 { return &v }
func groupFor(c ExecutionCandidate, p HeadroomPolicy) string {
	if p.Equivalence == FitnessBand {
		return ""
	}
	if p.Equivalence == DeclaredGroups {
		for i, g := range p.Groups {
			for _, pair := range g {
				if pair.Model == c.Model && pair.Effort == c.Effort && (pair.Runtime == nil || *pair.Runtime == c.Runtime) {
					return fmt.Sprintf("declared-%d", i)
				}
			}
		}
		return ""
	}
	// TODO(decision): binding identity and usage identity represent the home;
	// all other execution members must match for same-pair-any-home.
	c.ID = ""
	c.RuntimeBindingID = ""
	c.UsageKey = nil
	c.BillingClass = ""
	// Version the canonical profile payload without changing execution equality.
	raw, err := canonical.Marshal(struct {
		SchemaVersion string             `json:"schema_version"`
		Candidate     ExecutionCandidate `json:"candidate"`
	}{"v1", c})
	if err != nil {
		// Unreachable for a validated snapshot; never merge candidates on error.
		return ""
	}
	digest := sha256.Sum256(raw)
	return "same-pair:" + hex.EncodeToString(digest[:])[:12]
}

// quantities treats a model scope as unmapped only when no scope-map entry
// exists for the runtime. An entry excluding this model is inapplicable, without
// quota_scope_unmapped; explain retains the window with applicable:false.
func quantities(c ExecutionCandidate, pos int64, s CandidateSnapshot, p HeadroomPolicy, role string) CandidateExplanation {
	x := CandidateExplanation{CandidateID: c.ID, BillingClass: c.BillingClass, BasePosition: pos, Group: groupFor(c, p), Windows: []WindowExplanation{}, ReasonCodes: []ReasonCode{}}
	if c.BillingClass != BillingSubscription {
		return x
	}
	var fact *UsageFact
	for i := range s.UsageFacts {
		if s.UsageFacts[i].Key == *c.UsageKey && s.UsageFacts[i].Runtime == c.Runtime {
			fact = &s.UsageFacts[i]
			break
		}
	}
	foundCount := false
	for _, v := range s.Inflight {
		if v.Key == *c.UsageKey {
			x.Inflight = v.Runs
			foundCount = true
			break
		}
	}
	if !foundCount {
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonInflightUnknown)
	}
	readable := fact != nil && fact.State != StateAbsent && fact.State != StateNotSupported && fact.State != StateUnavailable
	if !readable {
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaUnknown)
	}
	applicable, known, timed := 0, 0, 0
	if fact != nil {
		x.Credits = fact.Credits
		for _, w := range fact.Windows {
			wx := WindowExplanation{Window: w}
			mapped := w.Scope == "all"
			if !mapped {
				mappingFound := false
				for _, entry := range s.ScopeMap.Entries {
					if entry.Runtime == c.Runtime && entry.Scope == w.Scope {
						mappingFound = true
						mapped = slices.Contains(entry.ModelIDs, c.Model)
						break
					}
				}
				if !mappingFound {
					x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaScopeUnmapped)
				}
			}
			wx.Applicable = mapped && (p.Windows.All || slices.Contains(p.Windows.IDs, w.ID))
			if wx.Applicable {
				applicable++
				wx.Known = readable && w.Freshness == Fresh && ClassifyFreshness(w, s.AsOf, MaxTTL) != Invalid && (w.ResetsAt == nil || *w.ResetsAt > s.AsOf)
				if wx.Known {
					known++
					r := max(int64(0), 10000-*w.UsedBP)
					wx.Remaining = ptr64(r)
					if x.Headroom == nil || r < *x.Headroom {
						x.Headroom = ptr64(r)
					}
					if *w.UsedBP >= 10000 {
						x.Over = true
					}
					if w.ResetsAt != nil {
						timed++
						tau := min(*w.ResetsAt-s.AsOf, w.Minutes*60) * 10000 / (w.Minutes * 60)
						wx.TimeLeft = ptr64(tau)
						wx.Slack = ptr64(r - tau)
						if x.Slack == nil || *wx.Slack < *x.Slack {
							x.Slack = ptr64(*wx.Slack)
						}
					}
				} else {
					switch w.Freshness {
					case Stale:
						x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaStale)
					case Expired:
						x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaExpired)
					case Invalid:
						x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaInvalid)
					}
				}
			}
			x.Windows = append(x.Windows, wx)
		}
	}
	if applicable == 0 {
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaNoApplicableWindow)
	}
	if known > 0 && known < applicable {
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaPartial)
	}
	if applicable == 0 || known != applicable {
		x.Headroom = nil
		x.Slack = nil
	} else if timed != applicable {
		x.Slack = nil
	}
	if x.Slack != nil {
		x.Band = floorDiv(*x.Slack, p.BandWidthBP)
	}
	if x.Headroom != nil && *x.Headroom < p.ReserveBP && !slices.Contains(p.ProtectedRoles, role) {
		x.Reserve = true
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaReserve)
	}
	if x.Over {
		x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaOver)
	}
	// Pace classifications require measured slack; incomplete facts stay neutral.
	if x.Slack != nil {
		switch {
		case x.Band >= p.ExpiringBand:
			x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaExpiring)
		case x.Band <= p.ConserveBand:
			x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaConserve)
		default:
			x.ReasonCodes = addReason(x.ReasonCodes, ReasonQuotaOnPace)
		}
	}
	return x
}
func (h HeadroomStrategy) evaluate(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, t TaskEnvelope) (SelectionResult, Explanation, error) {
	return h.evaluateVersion(s, e, p, t, p.Quality != nil || h.selectorVersion() == QualitySelectorVersion)
}

func (h HeadroomStrategy) evaluateVersion(s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, t TaskEnvelope, quality bool) (SelectionResult, Explanation, error) {
	ex := Explanation{SchemaVersion: s.SchemaVersion, AsOf: s.AsOf, Candidates: []CandidateExplanation{}}
	version := SelectorVersion
	if quality {
		version = QualitySelectorVersion
	}
	if err := validateQualitySelectorInput(&s, &e, p, version, nil); err != nil {
		return SelectionResult{}, ex, err
	}
	if !quality {
		if err := s.Validate(); err != nil {
			return SelectionResult{}, ex, err
		}
		if err := p.Validate(); err != nil {
			return SelectionResult{}, ex, err
		}
	}
	if !quality && p.Headroom.Equivalence == FitnessBand {
		if e.EstimatorPartition == nil {
			return SelectionResult{}, ex, fail(HeadroomPartitionRequired, "fitness-band requires an estimator partition")
		}
		if err := e.ValidateAgainst(s.Candidates); err != nil {
			return SelectionResult{}, ex, err
		}
	}
	if h.Base == nil {
		return SelectionResult{}, ex, fail("routing_invalid_selection", "headroom requires a base strategy")
	}
	base, err := h.Base.Select(s, e, p, t)
	if err != nil {
		return base, ex, err
	}
	if err = base.ValidateAgainst(s.Candidates); err != nil {
		return base, ex, err
	}
	if base.Abstention != nil {
		ex.Abstention = base.Abstention
		return base, ex, nil
	}
	if len(base.Order) == 0 {
		return base, ex, nil
	}
	if quality {
		if err := validateQualityHeadroomComposition(s, e, p, base); err != nil {
			return SelectionResult{}, ex, err
		}
	}
	order := make([]RankedCandidate, len(base.Order))
	copy(order, base.Order)
	groups := [][]int{}
	groupIDs := []string{}
	partitionGroups := map[string]string{}
	if p.Headroom.Equivalence == FitnessBand {
		for _, group := range e.EstimatorPartition.Groups {
			for _, id := range group.CandidateIDs {
				partitionGroups[id] = group.ID
			}
		}
	}
	for i, r := range base.Order {
		var c ExecutionCandidate
		for _, v := range s.Candidates {
			if v.ID == r.CandidateID {
				c = v
				break
			}
		}
		x := quantities(c, int64(i), s, p.Headroom, t.Role)
		if p.Headroom.Equivalence == FitnessBand {
			x.Group = partitionGroups[c.ID]
		}
		for _, code := range r.ReasonCodes {
			x.ReasonCodes = addReason(x.ReasonCodes, code)
		}
		ex.Candidates = append(ex.Candidates, x)
		order[i].ReasonCodes = slices.Clone(x.ReasonCodes)
		order[i].BasePosition = ptr64(int64(i))
		if c.BillingClass == BillingSubscription && x.Group != "" {
			j := slices.Index(groupIDs, x.Group)
			if j < 0 {
				j = len(groups)
				groupIDs = append(groupIDs, x.Group)
				groups = append(groups, []int{})
			}
			groups[j] = append(groups[j], i)
		}
	}
	for _, slots := range groups {
		members := slices.Clone(slots)
		slices.SortFunc(members, func(a, b int) int {
			return compareHeadroom(ex.Candidates[a], ex.Candidates[b])
		})
		// Annotate both sides only when in-flight is the deciding comparison step.
		for a := 0; a < len(members); a++ {
			for b := a + 1; b < len(members); b++ {
				x, y := &ex.Candidates[members[a]], &ex.Candidates[members[b]]
				if x.Over == y.Over && x.Reserve == y.Reserve && x.Band == y.Band && x.Inflight != y.Inflight {
					x.ReasonCodes = addReason(x.ReasonCodes, ReasonInflightSpread)
					y.ReasonCodes = addReason(y.ReasonCodes, ReasonInflightSpread)
				}
			}
		}
		for j, m := range members {
			order[slots[j]] = RankedCandidate{CandidateID: ex.Candidates[m].CandidateID, Position: int64(slots[j]), BasePosition: ptr64(ex.Candidates[m].BasePosition), ReasonCodes: slices.Clone(ex.Candidates[m].ReasonCodes)}
		}
	}
	// TODO(decision): "every candidate" means the entire admitted set, so an
	// unknown, local or metered member prevents all-reserved abstention.
	allReserved := len(ex.Candidates) > 0
	for _, x := range ex.Candidates {
		allReserved = allReserved && x.Reserve
	}
	if allReserved {
		for i := range ex.Candidates {
			ex.Candidates[i].ReasonCodes = addReason(ex.Candidates[i].ReasonCodes, ReasonReserveBreached)
		}
		for i := range order {
			order[i].ReasonCodes = addReason(order[i].ReasonCodes, ReasonReserveBreached)
		}
		if p.Headroom.OnAllReserved == ReservedAbstain {
			ex.Abstention = &Abstention{Reason: ReasonReserveBreached}
			return SelectionResult{Abstention: ex.Abstention}, ex, nil
		}
	}
	for i := range ex.Candidates {
		for _, r := range order {
			if r.CandidateID == ex.Candidates[i].CandidateID {
				ex.Candidates[i].FinalPosition = ptr64(r.Position)
			}
		}
	}
	return SelectionResult{Order: order}, ex, nil
}

// compareHeadroom implements the six-step §5.3 key.
func compareHeadroom(x, y CandidateExplanation) int {
	switch {
	case x.Over != y.Over:
		if x.Over {
			return 1
		}
		return -1
	case x.Reserve != y.Reserve:
		if x.Reserve {
			return 1
		}
		return -1
	case x.Band != y.Band:
		if x.Band > y.Band {
			return -1
		}
		return 1
	case x.Inflight != y.Inflight:
		if x.Inflight < y.Inflight {
			return -1
		}
		return 1
	case x.BasePosition != y.BasePosition:
		if x.BasePosition < y.BasePosition {
			return -1
		}
		return 1
	}
	// Safety net: unique base positions make this id tie-break unreachable.
	if x.CandidateID < y.CandidateID {
		return -1
	}
	if x.CandidateID > y.CandidateID {
		return 1
	}
	return 0
}
