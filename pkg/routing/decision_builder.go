package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

const SelectorVersion = "router-core-v1"

// DecisionOptions are stored replay inputs. No timestamp is read by the router.
type DecisionOptions struct {
	// TODO(decision): explicit adapter fallback options live in the replay bundle
	// because the frozen policy has no fallback members.
	BaselineFallbackReasons []ReasonCode `json:"baseline_fallback_reasons,omitempty"`
	MandatoryEvidenceFloor  bool         `json:"mandatory_evidence_floor,omitempty"`
	AssessorVersion         string       `json:"assessor_version"`
	EstimatorVersion        string       `json:"estimator_version"`
	SelectorVersion         string       `json:"selector_version"`
	ApplicabilityScope      string       `json:"applicability_scope"`
	ExpiresAt               *int64       `json:"expires_at,omitempty"`
}
type DecisionBundle struct {
	SchemaVersion string             `json:"schema_version"`
	Envelope      TaskEnvelope       `json:"envelope"`
	Snapshot      CandidateSnapshot  `json:"snapshot"`
	Evaluation    EvaluationSnapshot `json:"evaluation"`
	Policy        RoutingPolicy      `json:"policy"`
	Versions      DecisionOptions    `json:"versions"`
}

// LoadBundle strictly decodes the supplied bytes; only the CLI reads files.
func LoadBundle(raw []byte) (DecisionBundle, error) {
	var wire struct {
		SchemaVersion string             `json:"schema_version"`
		Envelope      TaskEnvelope       `json:"envelope"`
		Snapshot      CandidateSnapshot  `json:"snapshot"`
		Evaluation    EvaluationSnapshot `json:"evaluation"`
		Policy        json.RawMessage    `json:"policy"`
		Versions      DecisionOptions    `json:"versions"`
	}
	if _, err := canonical.Canonicalize(raw); err != nil {
		return DecisionBundle{}, err
	}
	if err := decodeStrict(raw, &wire); err != nil {
		return DecisionBundle{}, err
	}
	p, err := loadPolicy(wire.Policy, wire.Versions.SelectorVersion != SelectorVersion)
	if err != nil {
		return DecisionBundle{}, err
	}
	b := DecisionBundle{wire.SchemaVersion, wire.Envelope, wire.Snapshot, wire.Evaluation, p, wire.Versions}
	if b.SchemaVersion == "" {
		return b, fail(ContractEmptyField, "bundle schema_version is required")
	}
	// Mode off evaluates nothing (decision-bundle.md, R7), so the partition
	// boundary is checked only where a strategy runs or a decision is built.
	if b.Policy.Mode != ModeOff {
		if err := validateBundleBoundary(b); err != nil {
			return b, err
		}
	}
	return b, nil
}
func decodeStrict(raw []byte, dst any) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fail("routing_invalid_input", "invalid JSON")
	}
	if err := checkJSONKeys(v, reflect.TypeOf(dst).Elem(), "$"); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fail("routing_invalid_input", err.Error())
	}
	return nil
}

// LoadSnapshot and LoadDecision validate frozen document shapes without I/O.
func LoadSnapshot(raw []byte) (CandidateSnapshot, error) {
	var s CandidateSnapshot
	if _, err := canonical.Canonicalize(raw); err != nil {
		return s, err
	}
	if err := decodeStrict(raw, &s); err != nil {
		return s, err
	}
	return s, s.Validate()
}
func LoadDecision(raw []byte) (RoutingDecision, error) {
	var d RoutingDecision
	if _, err := canonical.Canonicalize(raw); err != nil {
		return d, err
	}
	if err := decodeStrict(raw, &d); err != nil {
		return d, err
	}
	return d, d.Validate()
}

// BuildDecision binds the strategy result to all frozen inputs before fallback.
// Abstention stays abstention even when options authorize a later BaselineFallback.
// Alternatives carry base positions, while structured explain carries final positions.
func BuildDecision(t TaskEnvelope, s CandidateSnapshot, e EvaluationSnapshot, p RoutingPolicy, r SelectionResult, o DecisionOptions) (*RoutingDecision, error) {
	if p.Mode == ModeOff {
		return nil, nil
	}
	if err := validateBundleBoundary(DecisionBundle{Envelope: t, Snapshot: s, Evaluation: e, Policy: p, Versions: o}); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := r.ValidateAgainst(s.Candidates); err != nil {
		return nil, err
	}
	if o.SelectorVersion == QualitySelectorVersion {
		// Bind the supplied result to the versioned final selection, including
		// headroom, abstention and reasons, without recursing through Route.
		strategy, err := StrategyForVersion(p, o.SelectorVersion)
		if err != nil {
			return nil, err
		}
		expected, err := strategy.Select(s, e, p, t)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(r, expected) {
			return nil, fail(RoutingSelectionMismatch, "supplied selection differs from the versioned frozen selection")
		}
	}
	td, err := t.Digest()
	if err != nil {
		return nil, err
	}
	sd, err := s.Digest()
	if err != nil {
		return nil, err
	}
	ed, err := e.Digest()
	if err != nil {
		return nil, err
	}
	pd, err := p.Digest()
	if err != nil {
		return nil, err
	}
	// TODO(decision): role_context_digest binds role plus ordered context; the
	// complete task envelope separately binds criteria, tools and revision.
	rd, err := canonical.Digest(struct {
		SchemaVersion string   `json:"schema_version"`
		Role          string   `json:"role"`
		Context       []string `json:"context"`
	}{t.SchemaVersion, t.Role, t.Context})
	if err != nil {
		return nil, err
	}
	if o.SelectorVersion != SelectorVersion && o.SelectorVersion != QualitySelectorVersion {
		return nil, fail("routing_selector_version_mismatch", "unsupported selector version")
	}
	d := RoutingDecision{SchemaVersion: t.SchemaVersion, TaskEnvelopeDigest: td, RoleContextDigest: rd, CandidateSnapshotDigest: sd, EvaluationSnapshotDigest: ed, RoutingPolicyDigest: pd, AssessorVersion: o.AssessorVersion, EstimatorVersion: o.EstimatorVersion, SelectorVersion: o.SelectorVersion, SelectionOrigin: OriginRouter, ApplicabilityScope: o.ApplicabilityScope, PreparedAt: s.AsOf, ExpiresAt: o.ExpiresAt, Alternatives: []RankedCandidate{}, ReasonCodes: []ReasonCode{}, EvidenceRefs: slices.Clone(e.Measurements)}
	for _, estimate := range e.Estimates {
		d.EvidenceRefs = append(d.EvidenceRefs, estimate.ContributingRecordIDs...)
	}
	slices.Sort(d.EvidenceRefs)
	d.EvidenceRefs = slices.Compact(d.EvidenceRefs)
	switch {
	case r.Abstention != nil:
		d.Outcome = OutcomeAbstain
		d.ReasonCodes = addReason(d.ReasonCodes, r.Abstention.Reason)
	case len(r.Order) == 0:
		d.Outcome = OutcomeNoEligibleCandidates
		d.ReasonCodes = addReason(d.ReasonCodes, ReasonNoEligibleCandidates)
	default:
		d.Outcome = OutcomeSelected
		for _, c := range s.Candidates {
			if c.ID == r.Order[0].CandidateID {
				selected := c
				d.SelectedCandidate = &selected
				break
			}
		}
		d.ReasonCodes = slices.Clone(r.Order[0].ReasonCodes)
	}

	if o.SelectorVersion == QualitySelectorVersion && p.Quality != nil {
		d.ReasonCodes = addReason(d.ReasonCodes, ReasonQualityFloorApplied)
	}
	abstainReasons := map[string][]ReasonCode{}
	if r.Abstention != nil && (p.Headroom.Enabled || o.SelectorVersion == QualitySelectorVersion) {
		ex, explainErr := Explain(DecisionBundle{Envelope: t, Snapshot: s, Evaluation: e, Policy: p, Versions: o})
		if explainErr != nil {
			return nil, explainErr
		}
		for _, x := range ex.Candidates {
			abstainReasons[x.CandidateID] = x.ReasonCodes
		}
	}
	// Current positions come from the result; retain wrapper-provided base positions.
	for _, ranked := range r.Order {
		if d.SelectedCandidate != nil && ranked.CandidateID == d.SelectedCandidate.ID {
			continue
		}
		alternative := ranked
		alternative.ReasonCodes = slices.Clone(ranked.ReasonCodes)
		if ranked.BasePosition != nil {
			alternative.BasePosition = ptr64(*ranked.BasePosition)
		}
		d.Alternatives = append(d.Alternatives, alternative)
	}
	if r.Abstention != nil {
		for i, id := range s.ConfigOrder {
			codes := slices.Clone(abstainReasons[id])
			if codes == nil {
				codes = []ReasonCode{}
			}
			codes = addReason(codes, r.Abstention.Reason)
			d.Alternatives = append(d.Alternatives, RankedCandidate{CandidateID: id, Position: int64(i), ReasonCodes: codes})
		}
	}
	result, err := d.WithContentID()
	if err != nil {
		return nil, err
	}
	return &result, nil
}

type RouteResult struct {
	Decision           *RoutingDecision    `json:"decision,omitempty"`
	EffectiveCandidate *ExecutionCandidate `json:"effective_candidate,omitempty"`
}

// ApplyMode is the caller adapter seam: off/shadow/recommend keep the baseline;
// select uses the decision and launches nothing for non-selected outcomes.
func ApplyMode(mode Mode, d *RoutingDecision, baseline *ExecutionCandidate) (*ExecutionCandidate, error) {
	switch mode {
	case ModeOff, ModeShadow, ModeRecommend:
		return baseline, nil
	case ModeSelect:
		if d == nil || d.Outcome != OutcomeSelected {
			return nil, nil
		}
		return d.SelectedCandidate, nil
	default:
		return nil, fail("routing_unknown_enum", "unknown mode")
	}
}

// Route runs the strategy, builds its decision, then optionally records fallback.
func Route(b DecisionBundle) (RouteResult, error) {
	var baseline *ExecutionCandidate
	if len(b.Snapshot.ConfigOrder) > 0 {
		for i := range b.Snapshot.Candidates {
			if b.Snapshot.Candidates[i].ID == b.Snapshot.ConfigOrder[0] {
				baseline = &b.Snapshot.Candidates[i]
				break
			}
		}
	}
	if b.Policy.Mode == ModeOff {
		return RouteResult{EffectiveCandidate: baseline}, nil
	}
	if err := validateBundleBoundary(b); err != nil {
		return RouteResult{}, err
	}
	strategy, err := StrategyForVersion(b.Policy, b.Versions.SelectorVersion)
	if err != nil {
		return RouteResult{}, err
	}
	r, err := strategy.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil {
		return RouteResult{}, err
	}
	d, err := BuildDecision(b.Envelope, b.Snapshot, b.Evaluation, b.Policy, r, b.Versions)
	if err != nil {
		return RouteResult{}, err
	}
	if d != nil && d.Outcome == OutcomeAbstain && baseline != nil && len(b.Versions.BaselineFallbackReasons) > 0 {
		fallback, fallbackErr := BaselineFallback(*d, b.Snapshot, *baseline, b.Versions.BaselineFallbackReasons, b.Versions.MandatoryEvidenceFloor || b.Policy.Quality != nil)
		if fallbackErr != nil {
			return RouteResult{}, fallbackErr
		}
		d = &fallback
	}
	effective, err := ApplyMode(b.Policy.Mode, d, baseline)
	return RouteResult{d, effective}, err
}

// BaselineFallback requires explicitly named reasons and a caller assertion that
// no mandatory evidence floor blocks it. It never turns no-candidates into a choice.
func BaselineFallback(d RoutingDecision, snapshot CandidateSnapshot, baseline ExecutionCandidate, allowed []ReasonCode, mandatoryFloor bool) (RoutingDecision, error) {
	if err := d.VerifyContentID(); err != nil {
		return RoutingDecision{}, err
	}
	digest, err := snapshot.Digest()
	if err != nil {
		return RoutingDecision{}, err
	}
	admitted := false
	for _, candidate := range snapshot.Candidates {
		if reflect.DeepEqual(candidate, baseline) {
			admitted = true
			break
		}
	}
	if digest != d.CandidateSnapshotDigest || !admitted {
		return RoutingDecision{}, fail("routing_fallback_not_allowed", "baseline does not match admitted snapshot")
	}
	if mandatoryFloor || slices.Contains(d.ReasonCodes, ReasonQualityFloorApplied) || d.Outcome != OutcomeAbstain || len(d.ReasonCodes) == 0 {
		return RoutingDecision{}, fail("routing_fallback_not_allowed", "baseline fallback is not allowed")
	}
	for _, reason := range d.ReasonCodes {
		if !slices.Contains(allowed, reason) {
			return RoutingDecision{}, fail("routing_fallback_not_allowed", "abstention reason is not allowed")
		}
	}
	d.Outcome = OutcomeSelected
	d.SelectionOrigin = OriginBaselineAfterAbstain
	d.SelectedCandidate = &baseline
	alternatives := []RankedCandidate{}
	for _, a := range d.Alternatives {
		if a.CandidateID != baseline.ID {
			alternatives = append(alternatives, a)
		}
	}
	d.Alternatives = alternatives
	return d.WithContentID()
}

func Explain(b DecisionBundle) (Explanation, error) {
	if err := validateBundleBoundary(b); err != nil {
		return Explanation{}, err
	}
	ex, err := explain(b)
	if err == nil && b.Versions.SelectorVersion == QualitySelectorVersion {
		ex = enrichQualityExplanation(ex, b)
	}
	return ex, err
}

func explain(b DecisionBundle) (Explanation, error) {
	base, err := StrategyForVersion(b.Policy, b.Versions.SelectorVersion)
	if err != nil {
		return Explanation{}, err
	}
	if h, ok := base.(qualityHeadroomStrategy); ok {
		_, ex, err := (HeadroomStrategy{Base: h.Base}).evaluateVersion(b.Snapshot, b.Evaluation, b.Policy, b.Envelope, true)
		return ex, err
	}
	if h, ok := base.(HeadroomStrategy); ok {
		_, ex, err := h.evaluate(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
		return ex, err
	}
	if err := b.Snapshot.Validate(); err != nil {
		return Explanation{}, err
	}
	r, err := base.Select(b.Snapshot, b.Evaluation, b.Policy, b.Envelope)
	if err != nil {
		return Explanation{}, err
	}
	ex := Explanation{SchemaVersion: b.Snapshot.SchemaVersion, AsOf: b.Snapshot.AsOf, Candidates: []CandidateExplanation{}, Abstention: r.Abstention}
	for i, ranked := range r.Order {
		var c ExecutionCandidate
		for _, candidate := range b.Snapshot.Candidates {
			if candidate.ID == ranked.CandidateID {
				c = candidate
				break
			}
		}
		x := quantities(c, int64(i), b.Snapshot, b.Policy.Headroom, b.Envelope.Role)
		x.Group = ""
		x.FinalPosition = ptr64(int64(i))
		x.ReasonCodes = slices.Clone(r.Order[i].ReasonCodes)
		ex.Candidates = append(ex.Candidates, x)
	}
	return ex, nil
}

// RenderHuman consumes only the structured explanation, never evaluator prose.
func (ex Explanation) RenderHuman() string {
	var out strings.Builder
	fmt.Fprintf(&out, "as_of=%d\n", ex.AsOf)
	if ex.Outcome != "" {
		fmt.Fprintf(&out, "outcome=%s origin=%s selected=%s\n", ex.Outcome, ex.SelectionOrigin, ex.SelectedCandidateID)
	}
	for _, x := range ex.Candidates {
		value := func(v *int64) string {
			if v == nil {
				return "unknown"
			}
			return fmt.Sprint(*v)
		}
		fmt.Fprintf(&out, "%s class=%s base=%d group=%s final=%s H=%s S=%s band=%d over=%t reserve=%t inflight=%d reasons=%v\n", x.CandidateID, x.BillingClass, x.BasePosition, x.Group, value(x.FinalPosition), value(x.Headroom), value(x.Slack), x.Band, x.Over, x.Reserve, x.Inflight, x.ReasonCodes)
		if x.Estimate != nil {
			fmt.Fprintf(&out, "  fitness=%s coverage=%s estimator=%s\n", value(x.Estimate.Fitness), value(x.Estimate.Coverage), x.Estimate.EstimatorVersion)
		}
		if x.Quality != nil {
			fmt.Fprintf(&out, "  passes_floor=%t reasons=%v\n", x.Quality.PassesFloor, x.Quality.ReasonCodes)
		}
		if x.Cost != nil {
			raw, _ := json.Marshal(x.Cost)
			fmt.Fprintf(&out, "  cost=%s\n", raw)
		}
		for _, w := range x.Windows {
			fmt.Fprintf(&out, "  %s scope=%s freshness=%s used=%s observed=%s reset=%s applicable=%t known=%t r=%s tau=%s s=%s\n", w.Window.ID, w.Window.Scope, w.Window.Freshness, value(w.Window.UsedBP), value(w.Window.ObservedAt), value(w.Window.ResetsAt), w.Applicable, w.Known, value(w.Remaining), value(w.TimeLeft), value(w.Slack))
		}
		if x.Credits != nil {
			raw, _ := json.Marshal(x.Credits)
			fmt.Fprintf(&out, "  credits=%s (display only)\n", raw)
		}
	}
	if ex.Abstention != nil {
		fmt.Fprintf(&out, "abstain: %s\n", ex.Abstention.Reason)
	}
	return out.String()
}
