// Package recommend selects catalog configurations from an already admitted set.
// It is pure: the caller supplies time, usage, admission, and decoded policy.
package recommend

import (
	"fmt"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

const (
	CatalogVersion        = "catalog-v1"
	PolicyVersion         = "recommend-policy-v1"
	DecisionVersion       = "recommend-decision-v1"
	SelectorVersion       = "recommend-v5"
	LegacySelectorVersion = "recommend-v3"
	BudgetSelectorVersion = "recommend-v4"
	PublicSelectorVersion = SelectorVersion
	OverlayVersion        = "overlay-v1"
	InvalidOverlay        = "invalid_catalog_overlay"
	NoQualifiedCandidate  = "no_qualified_candidate"
	InvalidCatalog        = "invalid_catalog"
	InvalidPolicy         = "invalid_policy"
	InvalidTask           = "invalid_task"
	InvalidInput          = "invalid_input"
	DecisionMismatch      = "decision_mismatch"
)

// Refusal is both a typed input error and a normal selection abstention.
type Refusal struct {
	Code    string `json:"code" toml:"code"`
	Message string `json:"message" toml:"message"`
}

func (e *Refusal) Error() string        { return e.Code + ": " + e.Message }
func refuse(code, message string) error { return &Refusal{code, message} }

// ReasoningContext is actual benchmark/target reasoning, independent of admission effort.
type ReasoningContext struct {
	Thinking string `json:"thinking" toml:"thinking"`
	Effort   string `json:"effort" toml:"effort"`
}

type Candidate struct {
	WeightsID         string            `json:"weights_id,omitempty" toml:"weights_id"`
	ExpectedWeightsID string            `json:"expected_weights_id,omitempty" toml:"expected_weights_id"`
	Reasoning         *ReasoningContext `json:"reasoning,omitempty" toml:"reasoning"`
	Runtime           string            `json:"runtime" toml:"runtime"`
	Model             string            `json:"model" toml:"model"`
	Effort            string            `json:"effort" toml:"effort"`
}

// Key is an unambiguous stable configuration identity, not a managed-home id.
func (c Candidate) Key() string {
	return fmt.Sprintf("%d:%s%d:%s%d:%s", len(c.Runtime), c.Runtime, len(c.Model), c.Model, len(c.Effort), c.Effort)
}

type QualityValue struct {
	Value  float64  `json:"value" toml:"value"`
	Kind   string   `json:"kind" toml:"kind"`
	Source string   `json:"source" toml:"source"`
	AsOf   string   `json:"as_of" toml:"as_of"`
	Stderr *float64 `json:"stderr,omitempty" toml:"stderr"`
	N      *int     `json:"n,omitempty" toml:"n"`
}
type Quality struct {
	Overall *QualityValue `json:"overall,omitempty" toml:"overall"`
	Coding  *QualityValue `json:"coding,omitempty" toml:"coding"`
	Review  *QualityValue `json:"review,omitempty" toml:"review"`
}
type Cost struct {
	USDProvenance    *ValueProvenance `json:"usd_per_task_provenance,omitempty" toml:"usd_per_task_provenance"`
	TokensProvenance *ValueProvenance `json:"tokens_per_task_provenance,omitempty" toml:"tokens_per_task_provenance"`
	USDPerTask       *float64         `json:"usd_per_task,omitempty" toml:"usd_per_task"`
	TokensPerTask    *int64           `json:"tokens_per_task,omitempty" toml:"tokens_per_task"`
	Kind             string           `json:"kind" toml:"kind"`
	Source           string           `json:"source" toml:"source"`
	AsOf             string           `json:"as_of" toml:"as_of"`
}
type Constraints struct {
	OnlyEffort string   `json:"only_effort,omitempty" toml:"only_effort"`
	NotFor     []string `json:"not_for,omitempty" toml:"not_for"`
}
type CatalogRow struct {
	Candidate
	Family      string               `json:"family" toml:"family"`
	Billing     routing.BillingClass `json:"billing" toml:"billing"`
	Quality     Quality              `json:"quality" toml:"quality"`
	Cost        Cost                 `json:"cost" toml:"cost"`
	Latency     string               `json:"latency,omitempty" toml:"latency"`
	Constraints Constraints          `json:"constraints" toml:"constraints"`
}
type Catalog struct {
	SchemaVersion string               `json:"schema_version" toml:"schema_version"`
	Provenance    []routing.Provenance `json:"provenance,omitempty" toml:"provenance"`
	Rows          []CatalogRow         `json:"rows" toml:"rows"`
}

type Tier string

const (
	TierS Tier = "S"
	TierA Tier = "A"
	TierB Tier = "B"
	TierC Tier = "C"
	TierU Tier = "U"
)

type TierEdges struct {
	S float64 `json:"s" toml:"s"`
	A float64 `json:"a" toml:"a"`
	B float64 `json:"b" toml:"b"`
}
type DifficultyRule struct {
	MinimumTier Tier   `json:"minimum_tier" toml:"minimum_tier"`
	Objective   string `json:"objective" toml:"objective"` // cheapest, standard, quality, quality_ignore_cost
}

// Policy is TOML-free. Construct programmatic policies with DefaultPolicy.
// Zero numeric/boolean overrides are values, except BurnFanOutCap whose zero
// is the omitted-field sentinel for the default of four.
type Policy struct {
	CatalogOverlay string `json:"catalog_overlay,omitempty" toml:"catalog_overlay"`
	// Empty BudgetMode is the legacy wire representation of balanced, preserving
	// the omitted balanced wire value. Use EffectiveBudgetMode to read it.
	BudgetMode            string                    `json:"budget_mode,omitempty" toml:"budget_mode"`
	BurnFanOutCap         int                       `json:"burn_fanout_cap,omitempty" toml:"burn_fanout_cap"`
	Host                  string                    `json:"host,omitempty" toml:"host"`
	Rules                 []Rule                    `json:"rules,omitempty" toml:"rules"`
	SchemaVersion         string                    `json:"schema_version" toml:"schema_version"`
	Mode                  routing.Mode              `json:"mode" toml:"mode"`
	AllowMetered          bool                      `json:"allow_metered" toml:"allow_metered"`
	TierEdges             TierEdges                 `json:"tier_edges" toml:"tier_edges"`
	ReviewTierEdges       TierEdges                 `json:"review_tier_edges" toml:"review_tier_edges"`
	Difficulties          map[string]DifficultyRule `json:"difficulties" toml:"difficulties"`
	StandardAMaxCostRatio float64                   `json:"standard_a_max_cost_ratio" toml:"standard_a_max_cost_ratio"`
	InterchangeableCostBP int64                     `json:"interchangeable_cost_bp" toml:"interchangeable_cost_bp"`
	FanOutK               int                       `json:"fanout_k" toml:"fanout_k"`
	Headroom              routing.HeadroomPolicy    `json:"headroom" toml:"headroom"`
}

type TaskProfile struct {
	Role        string `json:"role" toml:"role"`
	TaskClass   string `json:"task_class" toml:"task_class"`
	Difficulty  string `json:"difficulty,omitempty" toml:"difficulty"`
	Sensitivity string `json:"sensitivity,omitempty" toml:"sensitivity"`
	Language    string `json:"language,omitempty" toml:"language"`
	Pipeline    string `json:"pipeline,omitempty" toml:"pipeline"`
	// Platform is an optional caller facet for constraints such as not_for: windows.
	Platform string `json:"platform,omitempty" toml:"platform"`
}
type Locks struct {
	// Agent is the runtime lock supplied by --agent.
	Agent  string `json:"agent,omitempty" toml:"agent"`
	Model  string `json:"model,omitempty" toml:"model"`
	Effort string `json:"effort,omitempty" toml:"effort"`
}

// UsageSnapshot reuses the routing contracts, including caller-frozen freshness.
// UsageKeys maps configuration Key() to opaque subscription keys; an absent
// mapping means unknown usage, never a fabricated measurement.
type UsageSnapshot struct {
	AsOf      int64               `json:"as_of" toml:"as_of"`
	ScopeMap  routing.ScopeMap    `json:"scope_map" toml:"scope_map"`
	Facts     []routing.UsageFact `json:"facts" toml:"facts"`
	Inflight  []routing.Inflight  `json:"inflight" toml:"inflight"`
	UsageKeys map[string]string   `json:"usage_keys,omitempty" toml:"usage_keys"`
}

// AdmissionContext freezes the board selectors and each provider's confirmation
// policy from the same preflight that supplied candidate admission.
type AdmissionContext struct {
	BoardFlags        []string        `json:"board_flags,omitempty" toml:"board_flags"`
	RationaleRequired map[string]bool `json:"rationale_required,omitempty" toml:"rationale_required"`
}

type Request struct {
	SchemaVersion   string                 `json:"schema_version,omitempty" toml:"schema_version"`
	LocalCapability *LocalCapabilityBundle `json:"local_capability,omitempty" toml:"local_capability"`
	CatalogOverlay  *CatalogOverlay        `json:"catalog_overlay,omitempty" toml:"catalog_overlay"`
	OverlayDigest   string                 `json:"overlay_digest,omitempty" toml:"overlay_digest"`
	Admission       *AdmissionContext      `json:"admission,omitempty" toml:"admission"`
	Host            string                 `json:"host,omitempty" toml:"host"`
	Story           string                 `json:"story,omitempty" toml:"story"`
	ProducerFamily  string                 `json:"producer_family,omitempty" toml:"producer_family"`
	// Exclude contains exact runtime/model pairs supplied by --exclude.
	Exclude    []string      `json:"exclude,omitempty" toml:"exclude"`
	Catalog    Catalog       `json:"catalog" toml:"catalog"`
	Policy     Policy        `json:"policy" toml:"policy"`
	Task       TaskProfile   `json:"task" toml:"task"`
	Candidates []Candidate   `json:"candidates" toml:"candidates"`
	Locks      Locks         `json:"locks" toml:"locks"`
	Usage      UsageSnapshot `json:"usage" toml:"usage"`
	// AdmissionSource is caller provenance, e.g. candidates-file, spawn-preflight,
	// or path-catalog. The library never discovers or widens admission.
	AdmissionSource string `json:"admission_source" toml:"admission_source"`
}
type RankedCandidate struct {
	LocalRating  *LocalRating                  `json:"local_rating,omitempty" toml:"local_rating"`
	Candidate    Candidate                     `json:"candidate" toml:"candidate"`
	Family       string                        `json:"family" toml:"family"`
	Billing      routing.BillingClass          `json:"billing" toml:"billing"`
	Tier         Tier                          `json:"tier" toml:"tier"`
	Quality      *QualityValue                 `json:"quality,omitempty" toml:"quality"`
	QualityIndex string                        `json:"quality_index,omitempty" toml:"quality_index"`
	Cost         Cost                          `json:"cost" toml:"cost"`
	Frontier     bool                          `json:"frontier" toml:"frontier"`
	Headroom     *routing.CandidateExplanation `json:"headroom,omitempty" toml:"headroom"`
	ReasonCodes  []string                      `json:"reason_codes" toml:"reason_codes"`
}
type CandidateExplanation struct {
	RankedCandidate
	Admitted  bool `json:"admitted" toml:"admitted"`
	Qualified bool `json:"qualified" toml:"qualified"`
	Selected  bool `json:"selected" toml:"selected"`
}
type Recommendation struct {
	BudgetMode   string                 `json:"budget_mode,omitempty" toml:"budget_mode"`
	SkippedRules []SkippedRule          `json:"skipped_rules,omitempty" toml:"skipped_rules"`
	AppliedRules []AppliedRule          `json:"applied_rules,omitempty" toml:"applied_rules"`
	DecisionID   string                 `json:"decision_id,omitempty" toml:"decision_id"`
	Selected     *Candidate             `json:"selected,omitempty" toml:"selected"`
	Alternatives []RankedCandidate      `json:"alternatives" toml:"alternatives"`
	FanOut       []Candidate            `json:"fanout" toml:"fanout"`
	Refusal      *Refusal               `json:"refusal,omitempty" toml:"refusal"`
	Explanation  []CandidateExplanation `json:"explanation" toml:"explanation"`
}

// DecisionRecord contains all frozen inputs so it can be replayed without I/O.
type DecisionRecord struct {
	SchemaVersion   string         `json:"schema_version" toml:"schema_version"`
	SelectorVersion string         `json:"selector_version" toml:"selector_version"`
	DecisionID      string         `json:"decision_id,omitempty" toml:"decision_id"`
	Inputs          Request        `json:"inputs" toml:"inputs"`
	Recommendation  Recommendation `json:"recommendation" toml:"recommendation"`
}

func (r Recommendation) JSON() ([]byte, error) { return canonical.MarshalRecord(r) }
func (d DecisionRecord) JSON() ([]byte, error) { return canonical.Marshal(d) }
func (r Recommendation) RenderHuman() string {
	var b strings.Builder
	// The answer first: what to launch and the exact task-board flags.
	if r.Selected != nil {
		fmt.Fprintf(&b, "selected: %s/%s/%s\n", r.Selected.Runtime, r.Selected.Model, r.Selected.Effort)
		fmt.Fprintf(&b, "flags: --agent %s --model %s", r.Selected.Runtime, r.Selected.Model)
		if r.Selected.Effort != "none" {
			fmt.Fprintf(&b, " --reasoning-effort %s", r.Selected.Effort)
		}
		b.WriteByte('\n')
	}
	for i, c := range r.FanOut {
		fmt.Fprintf(&b, "fanout[%d]: %s/%s/%s\n", i, c.Runtime, c.Model, c.Effort)
	}
	fmt.Fprintf(&b, "decision=%s\n", r.DecisionID)
	if r.Refusal != nil {
		fmt.Fprintf(&b, "refused: %s\n", r.Refusal)
	}
	fmt.Fprintf(&b, "budget=%s\n", effectiveBudgetMode(r.BudgetMode))
	for _, rule := range r.SkippedRules {
		fmt.Fprintf(&b, "rule=%s source=%s rule_not_applicable: %s\n", rule.ID, rule.Source, rule.Facet)
	}
	for _, rule := range r.AppliedRules {
		fmt.Fprintf(&b, "rule=%s source=%s rationale=%s\n", rule.ID, rule.Source, rule.Rationale)
	}
	for _, x := range r.Explanation {
		q, source := "unknown", "unknown"
		if x.Quality != nil {
			q = fmt.Sprint(x.Quality.Value)
			source = x.Quality.Source
		}
		cost := "unknown"
		if x.Cost.USDPerTask != nil {
			cost = fmt.Sprintf("$%g/task", *x.Cost.USDPerTask)
		} else if x.Cost.TokensPerTask != nil {
			cost = fmt.Sprintf("%d tokens/task", *x.Cost.TokensPerTask)
		}
		fmt.Fprintf(&b, "%s/%s/%s tier=%s quality=%s index=%s source=%s cost=%s admitted=%t qualified=%t selected=%t frontier=%t reasons=%v\n", x.Candidate.Runtime, x.Candidate.Model, x.Candidate.Effort, x.Tier, q, x.QualityIndex, source, cost, x.Admitted, x.Qualified, x.Selected, x.Frontier, x.ReasonCodes)
		if x.LocalRating != nil {
			fmt.Fprintf(&b, "  local path=%s weights_id=%s evidence=%s selection_value=%s bundle=%s\n", x.LocalRating.Path, x.Candidate.WeightsID, x.LocalRating.EvidenceID, x.LocalRating.SelectionValue, x.LocalRating.BundleID)
		}
		if x.Headroom != nil {
			h := x.Headroom
			fmt.Fprintf(&b, "  headroom=%v slack=%v band=%d over=%t reserve=%t inflight=%d\n", value(h.Headroom), value(h.Slack), h.Band, h.Over, h.Reserve, h.Inflight)
		}
	}
	return b.String()
}
func value(p *int64) any {
	if p == nil {
		return "unknown"
	}
	return *p
}
