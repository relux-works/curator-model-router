package evidence

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

const (
	EstimatorInputSchemaVersion     = "fitness-input-v1"
	EstimatorConfigSchemaVersion    = "fitness-config-v1"
	EstimatorVersion                = routing.FitnessEstimatorVersion
	FitnessScale                    = "role-utility-bp-v1"
	EstimatorInputMismatch          = "estimator_input_mismatch"
	EstimatorRequirementsInvalid    = "estimator_requirements_invalid"
	EstimatorViewMismatch           = "estimator_view_mismatch"
	EstimatorRecordRefMismatch      = "estimator_record_ref_mismatch"
	EstimatorConfigInvalid          = "estimator_config_invalid"
	EstimatorPartitionInputMismatch = "estimator_partition_input_mismatch"
)

type EstimatorConfig struct {
	SchemaVersion       string `json:"schema_version"`
	Version             string `json:"version"`
	FitnessBandWidthBP  int64  `json:"fitness_band_width_bp"`
	CoverageBandWidthBP int64  `json:"coverage_band_width_bp"`
}

func DefaultEstimatorConfig() EstimatorConfig {
	return EstimatorConfig{EstimatorConfigSchemaVersion, EstimatorVersion, 1, 1}
}

func (c EstimatorConfig) Validate() error {
	if c.SchemaVersion != EstimatorConfigSchemaVersion || c.Version != EstimatorVersion || c.FitnessBandWidthBP < 1 || c.FitnessBandWidthBP > 10000 || c.CoverageBandWidthBP < 1 || c.CoverageBandWidthBP > 10000 {
		return refuse(EstimatorConfigInvalid, "unsupported estimator configuration or width outside 1..10000")
	}
	return nil
}

type CandidateFingerprint struct {
	CandidateID string      `json:"candidate_id"`
	Fingerprint Fingerprint `json:"fingerprint"`
}

// EstimatorInput is a complete in-memory capsule. Requirement weights are the
// already apportioned positive integer bp weights, frozen in both projections.
type EstimatorInput struct {
	SchemaVersion string                       `json:"schema_version"`
	Snapshot      Snapshot                     `json:"snapshot"`
	Imports       []Import                     `json:"imports"`
	Registry      Registry                     `json:"registry"`
	Requirements  RequirementsInput            `json:"requirements"`
	Derivation    Derivation                   `json:"derivation"`
	EvaluatedAt   string                       `json:"evaluated_at"`
	Candidates    []routing.ExecutionCandidate `json:"candidates"`
	Fingerprints  []CandidateFingerprint       `json:"fingerprints"`
	Config        EstimatorConfig              `json:"config"`
}

// ViewEstimator derives each distinct fingerprint once, including when several
// runtime homes admit the same variant. Its private data is never mutated.
type ViewEstimator struct {
	input          EstimatorInput
	view           View
	requirements   routing.Requirements
	estimates      []routing.Estimate
	snapshotDigest string
	inputDigest    string
	configDigest   string
	records        map[string]bool
}

var _ routing.FitnessEstimator = (*ViewEstimator)(nil)

// NewFitnessEstimator regenerates W2 and optionally verifies one stored view by
// complete canonical content, rather than trusting its inputs-only view id.
// A wholly omitted Go config uses explicit default widths before input hashing;
// any supplied config must contain both valid widths.
func NewFitnessEstimator(input EstimatorInput, storedView ...View) (*ViewEstimator, error) {
	if input.SchemaVersion != EstimatorInputSchemaVersion || input.Imports == nil || input.Candidates == nil || input.Fingerprints == nil {
		return nil, refuse(EstimatorInputMismatch, "unsupported or incomplete estimator input")
	}
	if input.Config == (EstimatorConfig{}) {
		input.Config = DefaultEstimatorConfig()
	}
	if err := input.Config.Validate(); err != nil {
		return nil, err
	}
	req, err := ProjectEstimatorRequirements(input.Requirements)
	if err != nil {
		return nil, err
	}
	if err = validateEstimatorDerivation(input.Derivation); err != nil {
		return nil, err
	}
	if err = input.Registry.Validate(); err != nil {
		return nil, err
	}
	sid, err := input.Snapshot.Digest()
	if err != nil {
		return nil, err
	}
	// Do not sort imports: the capsule itself must already be ordered by digest.
	ids := make([]string, 0, len(input.Imports))
	for _, doc := range input.Imports {
		id, err := doc.Digest()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = canonical.CheckOrdered(ids, func(s string) string { return s }); err != nil {
		return nil, err
	}
	if !estimatorEqual(ids, input.Snapshot.Imports) {
		return nil, refuse("evidence_snapshot_mismatch", "imports differ from frozen snapshot")
	}
	if err = canonical.CheckOrdered(input.Candidates, func(c routing.ExecutionCandidate) string { return c.ID }); err != nil {
		return nil, err
	}
	if err = canonical.CheckOrdered(input.Fingerprints, func(c CandidateFingerprint) string { return c.CandidateID }); err != nil {
		return nil, err
	}
	if len(input.Candidates) != len(input.Fingerprints) {
		return nil, refuse(EstimatorInputMismatch, "fingerprints must cover candidates exactly")
	}
	unique := map[string]Fingerprint{}
	for i, c := range input.Candidates {
		if err = c.Validate(); err != nil {
			return nil, err
		}
		b := input.Fingerprints[i]
		if b.CandidateID != c.ID || !estimatorFingerprintAgrees(c, b.Fingerprint) {
			return nil, refuse(EstimatorInputMismatch, "fingerprint differs from admitted variant")
		}
		unique[fingerprintKey(b.Fingerprint)] = b.Fingerprint
	}
	raw, err := canonical.Marshal(input)
	if err != nil {
		return nil, err
	}
	var frozen EstimatorInput
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	fingerprints := make([]Fingerprint, 0, len(unique))
	for _, f := range unique {
		fingerprints = append(fingerprints, f)
	}
	canonical.SortByKey(fingerprints, fingerprintKey)
	view, err := Derive(frozen.Snapshot, frozen.Imports, frozen.Registry, frozen.Requirements, frozen.Derivation, fingerprints, frozen.EvaluatedAt)
	if err != nil {
		return nil, err
	}
	if err = view.Validate(); err != nil {
		return nil, refuse(EstimatorViewMismatch, err.Error())
	}
	if len(storedView) > 1 {
		return nil, refuse(EstimatorViewMismatch, "at most one stored view may be supplied")
	}
	if len(storedView) == 1 && !estimatorEqual(view, storedView[0]) {
		return nil, refuse(EstimatorViewMismatch, "stored view differs from regenerated W2 content")
	}
	// Derive's inputs and candidates may share caller pointers; freeze its output.
	e := &ViewEstimator{input: frozen, view: estimatorCopy(view), requirements: req, snapshotDigest: sid, records: map[string]bool{}}
	e.inputDigest, err = canonical.Digest(frozen)
	if err != nil {
		return nil, err
	}
	e.configDigest, err = canonical.Digest(frozen.Config)
	if err != nil {
		return nil, err
	}
	for _, doc := range frozen.Imports {
		for _, o := range doc.Observations {
			e.records[o.ID] = true
		}
		for _, n := range doc.Notes {
			e.records[n.ID] = true
		}
	}
	e.estimates, err = e.project()
	if err != nil {
		return nil, err
	}
	return e, nil
}

// W2's generic validator permits caller-defined weights. This estimator only
// supports the reviewed role-suitability versions, whose note priors are pinned.
// Normalisations and the declared partial discount remain frozen caller inputs.
func validateEstimatorDerivation(d Derivation) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if d.Name != "role-suitability" || !member(d.Version, "1", "2") {
		return refuse("evidence_invalid_derivation", "unsupported estimator derivation name or version")
	}
	confidence := []Weight{{"high", 1}, {"low", 0.25}, {"medium", 0.6}}
	basis := []Weight{{"benchmark-reading", 0.4}, {"incident", 0.8}, {"operator-judgement", 0.6}, {"review-outcomes", 1}, {"run-outcomes", 0.8}}
	if !slices.Equal(d.ConfidenceWeights, confidence) || !slices.Equal(d.BasisWeights, basis) {
		return refuse("evidence_invalid_derivation", "changed note priors require a new reviewed derivation version")
	}
	return nil
}

// ProjectEstimatorRequirements refuses multi-valued facets instead of silently
// picking one. No category weight apportionment occurs inside the estimator.
func ProjectEstimatorRequirements(req RequirementsInput) (routing.Requirements, error) {
	out := routing.Requirements{Items: []routing.Requirement{}}
	for _, r := range req.Requirements {
		if !finite(r.Weight) || r.Weight < 1 || r.Weight > 10000 || math.Trunc(r.Weight) != r.Weight {
			return out, refuse(EstimatorRequirementsInvalid, "weights must be positive integer bp")
		}
	}
	if err := req.Validate(); err != nil {
		return out, err
	}
	if len(req.Requirements) == 0 {
		return out, refuse(EstimatorRequirementsInvalid, "nonempty requirements are required")
	}
	total := int64(0)
	for _, r := range req.Requirements {
		w := int64(r.Weight)
		total += w
		if total > 10000 {
			return out, refuse(EstimatorRequirementsInvalid, "requirement weights exceed 10000 bp")
		}
		f := map[string]string{}
		if r.Facets != nil {
			x := r.Facets
			if len(x.Languages) > 1 || len(x.Platforms) > 1 || len(x.Roles) > 1 {
				return out, refuse(EstimatorRequirementsInvalid, "facet cannot be represented as a scalar")
			}
			if len(x.Languages) == 1 {
				f["language"] = x.Languages[0]
			}
			if len(x.Platforms) == 1 {
				f["platform"] = x.Platforms[0]
			}
			if len(x.Roles) == 1 {
				f["role"] = x.Roles[0]
			}
			if x.ContextSize != "" {
				f["context_size"] = x.ContextSize
			}
			if x.Horizon != "" {
				f["horizon"] = x.Horizon
			}
		}
		if len(f) == 0 {
			f = nil
		}
		out.Items = append(out.Items, routing.Requirement{Category: r.Category, Facets: f, WeightBP: &w})
	}
	if total != 10000 {
		return out, refuse(EstimatorRequirementsInvalid, "requirement weights must sum to 10000 bp")
	}
	return out, nil
}

func estimatorFingerprintAgrees(c routing.ExecutionCandidate, f Fingerprint) bool {
	if f.ModelID != c.Model || (known(c.Effort) || known(f.Effort)) && c.Effort != f.Effort {
		return false
	}
	for _, p := range [][2]string{{c.Runtime, f.Runtime}, {c.Effort, f.Effort}, {c.ToolProfile, f.ToolProfile}} {
		if known(p[0]) && known(p[1]) && p[0] != p[1] {
			return false
		}
	}
	if c.EngineProfile != nil && f.EngineProfile != nil {
		a, b := c.EngineProfile, f.EngineProfile
		for _, p := range [][2]string{{a.Name, b.Name}, {a.EngineKind, b.EngineKind}, {a.WeightDigest, b.WeightDigest}, {a.Quantization, b.Quantization}} {
			if known(p[0]) && known(p[1]) && p[0] != p[1] {
				return false
			}
		}
		for _, p := range [][2]*int64{{a.KVContextTokens, b.KVContextTokens}, {a.PrefillChunkTokens, b.PrefillChunkTokens}} {
			if p[0] != nil && p[1] != nil && *p[0] != *p[1] {
				return false
			}
		}
	}
	// Harness version and execution profile are supplied public facts. In
	// particular execution_mode is deliberately not an execution-profile alias.
	return true
}

func estimatorEqual(a, b any) bool {
	wrap := func(v any) ([]byte, error) {
		return canonical.MarshalRecord(struct {
			Value any `json:"value"`
		}{v})
	}
	left, e1 := wrap(a)
	right, e2 := wrap(b)
	return e1 == nil && e2 == nil && bytes.Equal(left, right)
}

// Private frozen values have already passed canonical serialization. JSON copy
// preserves independent slices, maps, optional pointers and known-empty sets.
func estimatorCopy[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

func (e *ViewEstimator) Input() EstimatorInput              { return estimatorCopy(e.input) }
func (e *ViewEstimator) View() View                         { return estimatorCopy(e.view) }
func (e *ViewEstimator) Requirements() routing.Requirements { return estimatorCopy(e.requirements) }
func (e *ViewEstimator) InputDigest() string                { return e.inputDigest }
func (e *ViewEstimator) ConfigDigest() string               { return e.configDigest }
func (e *ViewEstimator) SnapshotDigest() string             { return e.snapshotDigest }

func (e *ViewEstimator) Estimate(req routing.Requirements, candidates []routing.ExecutionCandidate, ref routing.EvidenceSnapshotRef) ([]routing.Estimate, error) {
	if ref.Digest != e.snapshotDigest {
		return nil, routing.NewEstimatorSnapshotMismatchError()
	}
	if !estimatorEqual(req, e.requirements) || !estimatorEqual(candidates, e.input.Candidates) {
		return nil, refuse(EstimatorInputMismatch, "requirements or full candidates differ from frozen inputs")
	}
	if ref.RecordIDs == nil {
		return nil, refuse(EstimatorRecordRefMismatch, "record reference set is required")
	}
	if err := canonical.CheckOrdered(ref.RecordIDs, func(s string) string { return s }); err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	for _, id := range ref.RecordIDs {
		if !e.records[id] {
			return nil, refuse(EstimatorRecordRefMismatch, "record reference is not an observation or note in snapshot")
		}
		refs[id] = true
	}
	for _, estimate := range e.estimates {
		for _, id := range estimate.ContributingRecordIDs {
			if !refs[id] {
				return nil, refuse(EstimatorRecordRefMismatch, "record references omit an actual contribution")
			}
		}
	}
	return estimatorCopy(e.estimates), nil
}

func (e *ViewEstimator) Partition(estimates []routing.Estimate) (routing.EstimatorPartition, error) {
	p := routing.EstimatorPartition{SchemaVersion: routing.EstimatorPartitionSchemaVersion, EstimatorVersion: EstimatorVersion, Groups: []routing.EstimatorPartitionGroup{}}
	if !estimatorEqual(estimates, e.estimates) {
		return p, refuse(EstimatorPartitionInputMismatch, "partition requires this estimator's complete output")
	}
	groups := map[string][]string{}
	for _, v := range estimates {
		id := "singleton:" + v.CandidateID
		if v.Fitness != nil && v.Coverage != nil {
			digest, err := canonical.Digest(struct {
				SchemaVersion    string `json:"schema_version"`
				EstimatorVersion string `json:"estimator_version"`
				ConfigDigest     string `json:"config_digest"`
				FitnessBucket    int64  `json:"fitness_bucket"`
				CoverageBucket   int64  `json:"coverage_bucket"`
			}{"fitness-group-v1", EstimatorVersion, e.configDigest, *v.Fitness / e.input.Config.FitnessBandWidthBP, *v.Coverage / e.input.Config.CoverageBandWidthBP})
			if err != nil {
				return p, err
			}
			id = "fitness-band:" + strings.TrimPrefix(digest, "sha256:")
		}
		groups[id] = append(groups[id], v.CandidateID)
	}
	for id, members := range groups {
		sort.Strings(members)
		p.Groups = append(p.Groups, routing.EstimatorPartitionGroup{ID: id, CandidateIDs: members})
	}
	canonical.SortByKey(p.Groups, func(g routing.EstimatorPartitionGroup) string { return g.ID })
	return p, p.ValidateAgainst(e.input.Candidates, estimates)
}
