package routing

import "github.com/relux-works/curator-model-router/pkg/canonical"

const Unknown = canonical.Unknown

// TODO(decision): R3 names members but not their detailed shapes. Revision is an
// opaque substantive content revision; criteria/context/tools are string lists.
// All six task members are required; use empty lists for known-empty collections.
type TaskEnvelope struct {
	SchemaVersion       string   `json:"schema_version"`
	Description         string   `json:"description"`
	SubstantiveRevision string   `json:"substantive_revision"`
	Role                string   `json:"role"`
	Criteria            []string `json:"criteria"`
	Context             []string `json:"context"`
	Tools               []string `json:"tools"`
}
type ContextProfile struct {
	Name       string `json:"name"`
	LockSHA256 string `json:"lock_sha256"`
}

// TODO(decision): profile shapes are not fully specified in R3. EngineProfile
// carries the listed public fingerprint members; network is an opaque caller ref.
type EngineProfile struct {
	Name               string `json:"name"`
	EngineKind         string `json:"engine_kind"`
	WeightDigest       string `json:"weight_digest"`
	Quantization       string `json:"quantization"`
	KVContextTokens    *int64 `json:"kv_context_tokens,omitempty"`
	PrefillChunkTokens *int64 `json:"prefill_chunk_tokens,omitempty"`
}
type BillingClass string

const (
	BillingLocal        BillingClass = "local"
	BillingSubscription BillingClass = "subscription"
	BillingMetered      BillingClass = "metered"
)

type ExecutionCandidate struct {
	ID               string         `json:"id"`
	RuntimeBindingID string         `json:"runtime_binding_id"`
	Runtime          string         `json:"runtime"`
	Model            string         `json:"model"`
	Effort           string         `json:"effort"` // verbatim: never normalize or infer a default
	ContextProfile   ContextProfile `json:"context_profile"`
	Transport        string         `json:"transport"`
	ProviderID       string         `json:"provider_id"`
	EngineProfile    *EngineProfile `json:"engine_profile,omitempty"`
	NetworkProfile   *string        `json:"network_profile,omitempty"`
	ToolProfile      string         `json:"tool_profile"`
	ExecutionMode    string         `json:"execution_mode"`
	BillingClass     BillingClass   `json:"billing_class"`
	UsageKey         *string        `json:"usage_key,omitempty"` // required for subscription billing
}
type State string

const (
	StateExact        State = "exact"
	StatePercentOnly  State = "percent_only"
	StateLastObserved State = "last_observed"
	StateNotSupported State = "not_supported"
	StateUnavailable  State = "unavailable"
	StateAbsent       State = "absent"
)

type Freshness string

const (
	Fresh   Freshness = "fresh"
	Stale   Freshness = "stale"
	Expired Freshness = "expired"
	Invalid Freshness = "invalid"
)

type UsageWindow struct {
	ID         string    `json:"id"`
	Scope      string    `json:"scope"`
	Minutes    int64     `json:"minutes"`
	UsedBP     *int64    `json:"used_bp,omitempty"` // nil is unknown, zero is a measured value
	ResetsAt   *int64    `json:"resets_at,omitempty"`
	ObservedAt *int64    `json:"observed_at,omitempty"`
	Freshness  Freshness `json:"freshness"`
}
type Credits struct {
	Balance   *float64 `json:"balance,omitempty"`
	Unit      string   `json:"unit"`
	Unlimited *bool    `json:"unlimited,omitempty"`
}
type UsageFact struct {
	Key          string        `json:"key"`
	Runtime      string        `json:"runtime"`
	State        State         `json:"state"`
	Windows      []UsageWindow `json:"windows"`
	Credits      *Credits      `json:"credits,omitempty"`
	RecordDigest *string       `json:"record_digest,omitempty"` // required for a record; forbidden for absent
	FailureCount int64         `json:"failure_count"`
}
type ScopeMapEntry struct {
	Runtime  string   `json:"runtime"`
	Scope    string   `json:"scope"`
	ModelIDs []string `json:"model_ids"`
}
type ScopeMap struct {
	Version string          `json:"version"`
	Entries []ScopeMapEntry `json:"entries"`
}
type Inflight struct {
	Key  string `json:"key"`
	Runs int64  `json:"runs"`
}

// TODO(decision): snapshot provenance is an opaque source slug plus optional
// public version/digest. Paths and authentication data are never contract fields.
type Provenance struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// CandidateSnapshot keeps Candidates keyed and sorted by id. ConfigOrder is the
// required operator/caller configuration order, an exact permutation of those
// ids in semantic order, and the only source of base order for config-order.
// Both Candidates and ConfigOrder are included in the snapshot digest.
type CandidateSnapshot struct {
	SchemaVersion string               `json:"schema_version"`
	AsOf          int64                `json:"as_of"`
	ScopeMap      ScopeMap             `json:"scope_map"`
	UsageFacts    []UsageFact          `json:"usage_facts"`
	Inflight      []Inflight           `json:"inflight"`
	Candidates    []ExecutionCandidate `json:"candidates"`
	ConfigOrder   []string             `json:"config_order"`
	Source        Provenance           `json:"source"`
}

// TODO(decision): requirement/rubric/assessment shapes are minimally typed seams
// pending W2: category/facets/weight, public rubric identity, frozen features.
type Requirement struct {
	Category string            `json:"category"`
	Facets   map[string]string `json:"facets,omitempty"`
	WeightBP *int64            `json:"weight_bp,omitempty"`
}
type Requirements struct {
	Items []Requirement `json:"items"`
}
type Rubric struct {
	ID           string       `json:"id"`
	Version      string       `json:"version"`
	Requirements Requirements `json:"requirements"`
}
type Assessment struct {
	ID              string            `json:"id"`
	Requirements    Requirements      `json:"requirements"`
	Features        map[string]string `json:"features,omitempty"`
	AssessorVersion string            `json:"assessor_version"`
	ConfidenceBP    *int64            `json:"confidence_bp,omitempty"`
}

// EvidenceSnapshotRef keeps W1 independent of pkg/evidence. Record ids are
// obs:/note: addresses within this immutable snapshot, ordered lexically.
type EvidenceSnapshotRef struct {
	Digest    string   `json:"digest"`
	RecordIDs []string `json:"record_ids"`
}

// TODO(decision): fitness and coverage use a normalized 0..10000 bp scale.
// Fitness remains relative utility, never calibrated success probability.
// Estimate field names follow the brief; contributions are explicit record ids.
type Estimate struct {
	CandidateID           string   `json:"candidate_id"`
	Fitness               *int64   `json:"fitness,omitempty"`  // integer bp; absent = unknown, zero = value
	Coverage              *int64   `json:"coverage,omitempty"` // integer bp
	ContributingRecordIDs []string `json:"contributing_record_ids"`
	EstimatorVersion      string   `json:"estimator_version"`
}
type EvaluationSnapshot struct {
	SchemaVersion          string              `json:"schema_version"`
	EvidenceSnapshotDigest string              `json:"evidence_snapshot_digest"`
	Measurements           []string            `json:"measurements"` // public record addresses
	Rubric                 *Rubric             `json:"rubric,omitempty"`
	Assessments            []Assessment        `json:"assessments"`
	Estimates              []Estimate          `json:"estimates"`
	EvaluatorVersions      []Provenance        `json:"evaluator_versions"`
	EstimatorPartition     *EstimatorPartition `json:"estimator_partition,omitempty"`
	Costs                  *CostSnapshot       `json:"costs,omitempty"`
}

// FitnessEstimator must be pure, deterministic, context-free, with no clock,
// file, network, model, or other I/O. Evidence must already be frozen in memory;
// the implementation's composition root supplies its immutable evidence data.
// Implementations MUST return a typed *Error with code EstimatorSnapshotMismatch
// (via NewEstimatorSnapshotMismatchError) when EvidenceSnapshotRef.Digest differs
// from the digest of the immutable evidence held by the estimator.
// TODO(decision): the evidence seam is a digest/address reference, rather than a
// concrete W2 store type, to avoid coupling the parallel packages.
type FitnessEstimator interface {
	Estimate(Requirements, []ExecutionCandidate, EvidenceSnapshotRef) ([]Estimate, error)
}
type RankedCandidate struct {
	CandidateID string `json:"candidate_id"`
	Position    int64  `json:"position"`
	// BasePosition is the position in the base strategy's order before any
	// wrapper permuted it; absent when no wrapper ran. Within a selection order
	// or alternatives list, values are all-or-none, non-negative and unique; in a
	// selection order they also form a permutation of 0..n-1 (alternatives may
	// omit the selected candidate).
	BasePosition *int64       `json:"base_position,omitempty"`
	ReasonCodes  []ReasonCode `json:"reason_codes"`
}
type Abstention struct {
	Reason ReasonCode `json:"reason"`
}
type SelectionResult struct {
	Order      []RankedCandidate `json:"order,omitempty"`
	Abstention *Abstention       `json:"abstention,omitempty"`
}

// SelectionStrategy returns a total order of ALL eligible candidates (0..n-1)
// OR one abstention. It must read only its frozen inputs, with no clock or I/O.
type SelectionStrategy interface {
	Select(CandidateSnapshot, EvaluationSnapshot, RoutingPolicy, TaskEnvelope) (SelectionResult, error)
}

// TODO(decision): R7 names distinct outcomes without wire tokens for every one;
// use selected/abstain/no_eligible_candidates/timeout/invalid_response. R8 only
// fixes baseline_after_abstain; the other origins are router/baseline/manual.
type OutcomeKind string

const (
	OutcomeSelected             OutcomeKind = "selected"
	OutcomeAbstain              OutcomeKind = "abstain"
	OutcomeNoEligibleCandidates OutcomeKind = "no_eligible_candidates"
	OutcomeTimeout              OutcomeKind = "timeout"
	OutcomeInvalidResponse      OutcomeKind = "invalid_response"
)

type SelectionOrigin string

const (
	OriginRouter               SelectionOrigin = "router"
	OriginBaseline             SelectionOrigin = "baseline"
	OriginManual               SelectionOrigin = "manual"
	OriginBaselineAfterAbstain SelectionOrigin = "baseline_after_abstain"
)

// TODO(decision): R8 has no applicability-scope structure or timestamp encoding;
// scope is an opaque caller string; prepared_at/expires_at use UTC Unix seconds.
// SelectedCandidate uses the full admitted variant to bind tools and provider.
type RoutingDecision struct {
	DecisionID               string              `json:"decision_id,omitempty"`
	SchemaVersion            string              `json:"schema_version"`
	TaskEnvelopeDigest       string              `json:"task_envelope_digest"`
	RoleContextDigest        string              `json:"role_context_digest"`
	CandidateSnapshotDigest  string              `json:"candidate_snapshot_digest"`
	EvaluationSnapshotDigest string              `json:"evaluation_snapshot_digest"`
	RoutingPolicyDigest      string              `json:"routing_policy_digest"`
	AssessorVersion          string              `json:"assessor_version"`
	EstimatorVersion         string              `json:"estimator_version"`
	SelectorVersion          string              `json:"selector_version"`
	SelectedCandidate        *ExecutionCandidate `json:"selected_candidate,omitempty"`
	Alternatives             []RankedCandidate   `json:"alternatives"`
	ReasonCodes              []ReasonCode        `json:"reason_codes"`
	EvidenceRefs             []string            `json:"evidence_refs"` // obs:/note:/ret: addresses
	SelectionOrigin          SelectionOrigin     `json:"selection_origin"`
	ApplicabilityScope       string              `json:"applicability_scope"`
	PreparedAt               int64               `json:"prepared_at"`
	ExpiresAt                *int64              `json:"expires_at,omitempty"`
	Outcome                  OutcomeKind         `json:"outcome"`
}

func (v TaskEnvelope) Digest() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(v)
}
func (v CandidateSnapshot) Digest() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(v)
}
func (v EvaluationSnapshot) Digest() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(v)
}

// ContentID derives the id without mutating the decision or trusting an old id.
func (v RoutingDecision) ContentID() (string, error) {
	v.DecisionID = ""
	if err := v.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(v)
}
func (v RoutingDecision) Digest() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(v)
}

// WithContentID returns a copy with its immutable content-addressed identity.
func (v RoutingDecision) WithContentID() (RoutingDecision, error) {
	id, err := v.ContentID()
	if err != nil {
		return RoutingDecision{}, err
	}
	v.DecisionID = id
	return v, nil
}

// VerifyContentID refuses a missing, stale, or substituted decision identity.
// ContentID remains usable before an id is assigned and ignores any old id.
func (v RoutingDecision) VerifyContentID() error {
	id, err := v.ContentID()
	if err != nil {
		return err
	}
	if v.DecisionID != id {
		return fail(ContractDecisionIDMismatch, "decision_id does not match decision content id")
	}
	return v.Validate()
}
