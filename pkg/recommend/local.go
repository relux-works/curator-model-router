package recommend

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	LocalCapabilityVersion  = "local-capability-v1"
	CoefficientTableVersion = "quantization-coefficients-v1"
	LocalRequestVersion     = "recommend-request-v2"
	LocalDecisionVersion    = "recommend-decision-v2"
	LocalSelectorVersion    = "recommend-v6"
	InvalidLocalCapability  = "invalid_local_capability"
)

var weightsPattern = regexp.MustCompile(`^gguf-sha256:[0-9a-f]{64}$`)
var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,6})?$`)
var expiryPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
var payloadDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var reasoningEffortPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var basePattern = regexp.MustCompile(`^hf://models/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ValidateWeightsID validates syntax only; it makes no assertion about file bytes or load binding.
func ValidateWeightsID(id string) error {
	if !weightsPattern.MatchString(id) {
		return refuse(InvalidInput, "weights_id must match gguf-sha256: followed by 64 lowercase hex digits")
	}
	return nil
}
func validReasoning(r ReasoningContext) bool {
	return slices.Contains([]string{"on", "off", "unknown"}, r.Thinking) && reasoningEffortPattern.MatchString(r.Effort)
}
func matchingReasoning(a, b ReasoningContext) bool {
	return a == b && a.Thinking == "on" && a.Effort != "unknown"
}

// Decimal preserves the original JSON number token and uses fixed millionths.
// Transfer inputs reject exponents, signs and more than six fractional digits.
type Decimal string

func (d Decimal) units() (int64, error) {
	s := string(d)
	if !decimalPattern.MatchString(s) {
		return 0, fmt.Errorf("invalid nonnegative decimal %q", s)
	}
	whole, frac, _ := strings.Cut(s, ".")
	return strconv.ParseInt(whole+frac+strings.Repeat("0", 6-len(frac)), 10, 64)
}
func (d *Decimal) UnmarshalJSON(raw []byte) error {
	x := Decimal(raw)
	if _, err := x.units(); err != nil {
		return err
	}
	*d = x
	return nil
}
func (d Decimal) MarshalJSON() ([]byte, error) { _, err := d.units(); return []byte(d), err }
func decimalUnits(u int64) Decimal {
	return Decimal(fmt.Sprintf("%d.%06d", u/1000000, u%1000000))
}
func (d Decimal) number() float64 { v, _ := strconv.ParseFloat(string(d), 64); return v }

// LocalScore uses uncertainty in quality points. Estimates never claim a sample count.
type LocalScore struct {
	Value  Decimal `json:"value"`
	Stderr Decimal `json:"stderr"`
	Kind   string  `json:"kind"`
	Source string  `json:"source"`
	AsOf   string  `json:"as_of"`
	N      *int    `json:"n,omitempty"`
}

// BaseProvenance describes a locally supplied, licensed source export and calibration.
// Canonical base spelling is the source export's frozen HF API id, never guessed by the library.
type BaseProvenance struct {
	ProviderID       string  `json:"provider_id"`
	ProviderVersion  string  `json:"provider_version"`
	Terms            string  `json:"terms"`
	SourceModelID    string  `json:"source_model_id"`
	PayloadDigest    string  `json:"payload_digest"`
	RetrievedAt      string  `json:"retrieved_at"`
	Benchmark        string  `json:"benchmark"`
	BenchmarkVersion string  `json:"benchmark_version"`
	Split            string  `json:"split"`
	Metric           string  `json:"metric"`
	Unit             string  `json:"unit"`
	Direction        string  `json:"direction"`
	OriginalScore    Decimal `json:"original_score"`
	Calibration      string  `json:"calibration"`
}
type BaseModelRecord struct {
	ID         string                    `json:"id"`
	BaseID     string                    `json:"base_id"`
	Revision   string                    `json:"revision"` // literal unknown when unavailable
	Reasoning  ReasoningContext          `json:"reasoning"`
	Scope      string                    `json:"scope"` // exact task class or *
	Quality    map[string]LocalScore     `json:"quality"`
	Provenance map[string]BaseProvenance `json:"provenance"`
}
type MaterializationRecord struct {
	WeightsID    string             `json:"weights_id"`
	BaseRecordID string             `json:"base_record_id,omitempty"`
	Format       string             `json:"format"`
	Quantization string             `json:"quantization"`
	Lineage      string             `json:"lineage"`
	Measurements []LocalMeasurement `json:"measurements"`
}
type LocalMeasurement struct {
	ID        string           `json:"id"`
	Axis      string           `json:"axis"`
	Reasoning ReasoningContext `json:"reasoning"`
	Scope     string           `json:"scope"`
	Score     LocalScore       `json:"score"`
}
type QuantizationCoefficient struct {
	ID                  string           `json:"id"`
	WeightsID           string           `json:"weights_id"`
	BaseRecordID        string           `json:"base_record_id"`
	Quantization        string           `json:"quantization"`
	Axis                string           `json:"axis"`
	Scope               string           `json:"scope"`
	SourceReasoning     ReasoningContext `json:"source_reasoning"`
	TargetReasoning     ReasoningContext `json:"target_reasoning"`
	Method              string           `json:"method"`
	K                   Decimal          `json:"k"`
	CoefficientInterval []Decimal        `json:"coefficient_interval"`
	AddedStderr         Decimal          `json:"added_stderr"`
	Source              string           `json:"source"`
	Rationale           string           `json:"rationale"` // includes declared interval/uncertainty calibration
	AsOf                string           `json:"as_of"`
	ExpiresAt           string           `json:"expires_at"`
}
type CoefficientTable struct {
	SchemaVersion string                    `json:"schema_version"`
	Records       []QuantizationCoefficient `json:"records"`
}
type LocalCapabilityDocument struct {
	SchemaVersion    string                  `json:"schema_version"`
	Bases            []BaseModelRecord       `json:"bases"`
	Materializations []MaterializationRecord `json:"materializations"`
	Coefficients     CoefficientTable        `json:"coefficients"`
	SourcePriority   []string                `json:"source_priority"` // frozen ordered evidence IDs, used only within a precedence class
}

// JSON retains the exact opaque UTF-8 bytes as a string, including whitespace.
type LocalCapabilityBundle struct {
	ID   string `json:"id"`
	JSON string `json:"json"`
}

func FreezeLocalCapability(raw []byte) (LocalCapabilityBundle, error) {
	if _, err := LoadLocalCapability(raw); err != nil {
		return LocalCapabilityBundle{}, err
	}
	return LocalCapabilityBundle{ID: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), JSON: string(raw)}, nil
}
func (b LocalCapabilityBundle) Document() (LocalCapabilityDocument, error) {
	if b.ID != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(b.JSON))) {
		return LocalCapabilityDocument{}, refuse(InvalidLocalCapability, "evidence bundle byte digest differs")
	}
	return LoadLocalCapability([]byte(b.JSON))
}
func LoadLocalCapability(raw []byte) (LocalCapabilityDocument, error) {
	var d LocalCapabilityDocument
	if err := strict(raw, &d, InvalidLocalCapability); err != nil {
		return d, err
	}
	return d, d.Validate()
}
func LoadCoefficientTable(raw []byte) (CoefficientTable, error) {
	var t CoefficientTable
	if err := strict(raw, &t, InvalidLocalCapability); err != nil {
		return t, err
	}
	return t, t.Validate()
}
func validDate(s string) bool {
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Format("2006-01-02") == s
}
func validExpiry(s string) bool {
	_, err := time.Parse("2006-01-02T15:04:05Z", s)
	return expiryPattern.MatchString(s) && err == nil
}
func validAxis(s string) bool { return slices.Contains([]string{"overall", "coding", "review"}, s) }
func (s LocalScore) validate(measured bool, axis string) error {
	v, e1 := s.Value.units()
	_, e2 := s.Stderr.units()
	if e1 != nil || e2 != nil || v > 100000000 || !validDate(s.AsOf) || strings.TrimSpace(s.Source) == "" || !slices.Contains([]string{"measured", "estimate"}, s.Kind) {
		return refuse(InvalidLocalCapability, "invalid score or uncertainty/provenance")
	}
	if measured && s.Kind != "measured" || s.Kind == "estimate" && s.N != nil || s.N != nil && *s.N < 1 || axis == "review" && (s.Kind != "measured" || s.N == nil) {
		return refuse(InvalidLocalCapability, "invalid measurement/sample count; review is measurement-only")
	}
	return nil
}
func (t CoefficientTable) Validate() error {
	if t.SchemaVersion != CoefficientTableVersion || t.Records == nil {
		return refuse(InvalidLocalCapability, "invalid coefficient table version or missing records")
	}
	seen := map[string]bool{}
	for _, c := range t.Records {
		k, e := c.K.units()
		_, uerr := c.AddedStderr.units()
		if c.ID == "" || seen[c.ID] || ValidateWeightsID(c.WeightsID) != nil || c.BaseRecordID == "" || c.Quantization == "" || !validAxis(c.Axis) || c.Axis == "review" || c.Scope == "" || !validReasoning(c.SourceReasoning) || !validReasoning(c.TargetReasoning) || !matchingReasoning(c.SourceReasoning, c.TargetReasoning) || !slices.Contains([]string{"paired-measurement", "published-transfer", "operator-estimate"}, c.Method) || e != nil || uerr != nil || k > 1000000 || len(c.CoefficientInterval) != 2 || strings.TrimSpace(c.Source) == "" || strings.TrimSpace(c.Rationale) == "" || !validDate(c.AsOf) || !validExpiry(c.ExpiresAt) {
			return refuse(InvalidLocalCapability, "invalid coefficient, reasoning, interval or expiry")
		}
		lo, e1 := c.CoefficientInterval[0].units()
		hi, e2 := c.CoefficientInterval[1].units()
		if e1 != nil || e2 != nil || lo > k || k > hi || hi > 1000000 {
			return refuse(InvalidLocalCapability, "coefficient interval must contain k within [0,1]")
		}
		seen[c.ID] = true
	}
	return nil
}
func validateBases(bases []BaseModelRecord) error {
	seen, casing := map[string]bool{}, map[string]string{}
	for _, b := range bases {
		folded := strings.ToLower(b.BaseID)
		if b.ID == "" || seen[b.ID] || !basePattern.MatchString(b.BaseID) || casing[folded] != "" && casing[folded] != b.BaseID || b.Revision == "" || !validReasoning(b.Reasoning) || b.Scope == "" || len(b.Quality) == 0 || len(b.Quality) != len(b.Provenance) {
			return refuse(InvalidLocalCapability, "invalid/duplicate base or conflicting canonical spelling")
		}
		seen[b.ID] = true
		casing[folded] = b.BaseID
		for axis, s := range b.Quality {
			p, ok := b.Provenance[axis]
			if !validAxis(axis) || !ok {
				return refuse(InvalidLocalCapability, "missing axis calibration/provenance")
			}
			if err := s.validate(false, axis); err != nil {
				return err
			}
			if p.ProviderID == "" || p.ProviderVersion == "" || p.Terms == "" || p.SourceModelID == "" || !payloadDigestPattern.MatchString(p.PayloadDigest) || !validExpiry(p.RetrievedAt) || p.Benchmark == "" || p.BenchmarkVersion == "" || p.Split == "" || p.Metric == "" || p.Unit == "" || !slices.Contains([]string{"higher", "lower"}, p.Direction) || p.Calibration == "" {
				return refuse(InvalidLocalCapability, "incomplete source provenance or calibration")
			}
			if _, err := p.OriginalScore.units(); err != nil {
				return refuse(InvalidLocalCapability, err.Error())
			}
		}
	}
	return nil
}
func (d LocalCapabilityDocument) Validate() error {
	if d.SchemaVersion != LocalCapabilityVersion || d.Bases == nil || d.Materializations == nil || d.SourcePriority == nil {
		return refuse(InvalidLocalCapability, "invalid local document version or missing collections")
	}
	if err := validateBases(d.Bases); err != nil {
		return err
	}
	if err := d.Coefficients.Validate(); err != nil {
		return err
	}
	bases := map[string]BaseModelRecord{}
	for _, b := range d.Bases {
		bases[b.ID] = b
	}
	mats := map[string]MaterializationRecord{}
	ids := map[string]bool{}
	for _, m := range d.Materializations {
		if ValidateWeightsID(m.WeightsID) != nil || mats[m.WeightsID].WeightsID != "" || m.Format != "gguf" || m.Quantization == "" || m.Lineage == "" || m.Measurements == nil || m.BaseRecordID != "" && bases[m.BaseRecordID].ID == "" {
			return refuse(InvalidLocalCapability, "invalid materialization or base link")
		}
		mats[m.WeightsID] = m
		for _, v := range m.Measurements {
			if v.ID == "" || ids[v.ID] || !validAxis(v.Axis) || !validReasoning(v.Reasoning) || v.Scope == "" {
				return refuse(InvalidLocalCapability, "invalid or duplicate local measurement")
			}
			ids[v.ID] = true
			if err := v.Score.validate(true, v.Axis); err != nil {
				return err
			}
		}
	}
	for _, c := range d.Coefficients.Records {
		m := mats[c.WeightsID]
		b := bases[c.BaseRecordID]
		if ids[c.ID] || m.WeightsID == "" || m.BaseRecordID != c.BaseRecordID || m.Quantization != c.Quantization || b.ID == "" || b.Quality[c.Axis].Value == "" || !matchingReasoning(b.Reasoning, c.SourceReasoning) {
			return refuse(InvalidLocalCapability, "coefficient target/base/quantization conflict")
		}
		ids[c.ID] = true
	}
	priority := map[string]bool{}
	for _, id := range d.SourcePriority {
		if !ids[id] || priority[id] {
			return refuse(InvalidLocalCapability, "invalid source priority")
		}
		priority[id] = true
	}
	return nil
}

// LocalRating records the unpenalized value, conservative selection value and each transfer component.
type LocalRating struct {
	Path           string   `json:"path"`
	EvidenceID     string   `json:"evidence_id"`
	BundleID       string   `json:"bundle_id"`
	Axis           string   `json:"axis"`
	SelectionValue Decimal  `json:"selection_value"`
	BaseValue      *Decimal `json:"base_value,omitempty"`
	BaseStderr     *Decimal `json:"base_stderr,omitempty"`
	K              *Decimal `json:"k,omitempty"`
	AddedStderr    *Decimal `json:"added_stderr,omitempty"`
}

func transfer(base LocalScore, c QuantizationCoefficient) (LocalScore, Decimal, error) {
	b, _ := base.Value.units()
	k, _ := c.K.units()
	s, _ := base.Stderr.units()
	u, _ := c.AddedStderr.units()
	product := new(big.Int).Mul(big.NewInt(b), big.NewInt(k))
	product.Quo(product, big.NewInt(1000000))
	stderr := new(big.Int).Add(big.NewInt(s), big.NewInt(u))
	if !stderr.IsInt64() {
		return LocalScore{}, "", refuse(InvalidLocalCapability, "derived uncertainty overflows")
	}
	v, unc := product.Int64(), stderr.Int64()
	selection := new(big.Int).Sub(product, new(big.Int).Mul(stderr, big.NewInt(2)))
	if selection.Sign() < 0 {
		selection.SetInt64(0)
	}
	return LocalScore{Value: decimalUnits(v), Stderr: decimalUnits(unc), Kind: "estimate", Source: c.Source, AsOf: c.AsOf}, decimalUnits(selection.Int64()), nil
}
func localScope(scope, class string) bool { return scope == "*" || scope == class }
func priorityIndex(ids []string, id string) int {
	i := slices.Index(ids, id)
	if i < 0 {
		return len(ids)
	}
	return i
}
func (d LocalCapabilityDocument) resolve(c Candidate, class string, at int64, bundle string) (Quality, *LocalRating, string, error) {
	q := Quality{}
	if c.WeightsID == "" {
		return q, nil, "weights_identity_missing", nil
	}
	if c.ExpectedWeightsID == "" {
		return q, nil, "weights_pin_missing", nil
	}
	if c.ExpectedWeightsID != c.WeightsID {
		return q, nil, "weights_identity_mismatch", nil
	}
	if c.Reasoning == nil || c.Reasoning.Thinking != "on" {
		return q, nil, "quality_context_mismatch", nil
	}
	var mat MaterializationRecord
	for _, m := range d.Materializations {
		if m.WeightsID == c.WeightsID {
			mat = m
			break
		}
	}
	if mat.WeightsID == "" {
		return q, nil, "weights_unrated", nil
	}
	ratings := map[string]*LocalRating{}
	contextMismatch := false
	for _, m := range mat.Measurements {
		if localScope(m.Scope, class) && !matchingReasoning(m.Reasoning, *c.Reasoning) {
			contextMismatch = true
		}
	}
	for _, co := range d.Coefficients.Records {
		if co.WeightsID == c.WeightsID && localScope(co.Scope, class) && !matchingReasoning(co.TargetReasoning, *c.Reasoning) {
			contextMismatch = true
		}
	}
	for _, axis := range []string{"overall", "coding", "review"} {
		type choice struct {
			id          string
			rank        int
			score       LocalScore
			coefficient *QuantizationCoefficient
		}
		choices := []choice{}
		for _, m := range mat.Measurements {
			if m.Axis == axis && matchingReasoning(m.Reasoning, *c.Reasoning) && localScope(m.Scope, class) {
				choices = append(choices, choice{id: m.ID, score: m.Score})
			}
		}
		for i := range d.Coefficients.Records {
			co := &d.Coefficients.Records[i]
			exp, _ := time.Parse("2006-01-02T15:04:05Z", co.ExpiresAt)
			if co.WeightsID != c.WeightsID || co.Axis != axis || !matchingReasoning(co.TargetReasoning, *c.Reasoning) || !localScope(co.Scope, class) || at >= exp.Unix() {
				continue
			}
			for _, base := range d.Bases {
				if base.ID == co.BaseRecordID && localScope(base.Scope, class) {
					rank := 2
					if co.Method == "operator-estimate" {
						rank = 1
					}
					choices = append(choices, choice{id: co.ID, rank: rank, score: base.Quality[axis], coefficient: co})
				}
			}
		}
		slices.SortFunc(choices, func(a, b choice) int {
			if a.rank != b.rank {
				return a.rank - b.rank
			}
			return priorityIndex(d.SourcePriority, a.id) - priorityIndex(d.SourcePriority, b.id)
		})
		if len(choices) == 0 {
			continue
		}
		best := choices[0]
		if len(choices) > 1 && best.rank == choices[1].rank && priorityIndex(d.SourcePriority, best.id) == priorityIndex(d.SourcePriority, choices[1].id) {
			return q, nil, "", refuse(InvalidLocalCapability, "unresolved evidence priority tie")
		}
		score := best.score
		sel := score.Value
		rating := &LocalRating{Path: "exact_weights_measured", EvidenceID: best.id, BundleID: bundle, Axis: axis}
		if best.coefficient != nil {
			var err error
			score, sel, err = transfer(best.score, *best.coefficient)
			if err != nil {
				return q, nil, "", err
			}
			rating.Path = "base_quantization_transfer"
			rating.BaseValue = &best.score.Value
			rating.BaseStderr = &best.score.Stderr
			rating.K = &best.coefficient.K
			rating.AddedStderr = &best.coefficient.AddedStderr
		}
		rating.SelectionValue = sel
		ratings[axis] = rating
		st := score.Stderr.number()
		v := &QualityValue{Value: score.Value.number(), Stderr: &st, Kind: score.Kind, Source: score.Source, AsOf: score.AsOf, N: score.N}
		switch axis {
		case "overall":
			q.Overall = v
		case "coding":
			q.Coding = v
		case "review":
			q.Review = v
		}
	}
	index := qualityIndex(CatalogRow{Quality: q}, class)
	if index == "bughunt_fallback" {
		index = "review"
	}
	if r := ratings[index]; r != nil {
		return q, r, "", nil
	}
	if contextMismatch {
		return q, nil, "quality_context_mismatch", nil
	}
	return q, nil, "weights_unrated", nil
}

// LoadLocalRequest validates the new envelope before selection; no discovery or I/O.
func LoadLocalRequest(raw []byte) (Request, error) {
	var in Request
	if err := strict(raw, &in, InvalidInput); err != nil {
		return in, err
	}
	if in.SchemaVersion != LocalRequestVersion {
		return in, refuse(InvalidInput, "recommend-request-v2 required")
	}
	return normalizeRequest(in)
}

// LoadCandidates preserves the list-shaped admission file, with the same strict
// identity/member checks as request and catalog input.
func LoadCandidates(raw []byte) ([]Candidate, error) {
	var envelope struct {
		SchemaVersion string      `json:"schema_version"`
		Candidates    []Candidate `json:"candidates"`
	}
	wrapped := append([]byte(`{"schema_version":"recommend-candidates-v1","candidates":`), raw...)
	wrapped = append(wrapped, '}')
	if err := strict(wrapped, &envelope, InvalidInput); err != nil {
		return nil, err
	}
	for _, c := range envelope.Candidates {
		if !validCandidate(c) {
			return nil, refuse(InvalidInput, "incomplete admitted candidate")
		}
	}
	return envelope.Candidates, nil
}
