package evidence

import (
	"fmt"
	"github.com/relux-works/curator-model-router/pkg/canonical"
	"math"
	"strconv"
	"strings"
)

type Requirement struct {
	Category string  `json:"category"`
	Facets   *Facets `json:"facets,omitempty"`
	Weight   float64 `json:"weight"`
}
type RoleMapping struct {
	Role         string        `json:"role"`
	Requirements []Requirement `json:"requirements"`
}
type CategoryMapping struct {
	SchemaVersion string        `json:"schema_version"`
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	Roles         []RoleMapping `json:"roles"`
	Workloads     []RoleMapping `json:"workloads"`
}

// TODO(decision): these weights are explicit priors, pending playbook review.
// Workload mappings describe requirements, never candidate admission.
func DefaultMapping() CategoryMapping {
	return CategoryMapping{SchemaVersion, "playbook-categories", "1", []RoleMapping{
		{"developer", []Requirement{{Category: "code.fix", Weight: 2}, {Category: "code.implement", Weight: 3}, {Category: "code.refactor", Weight: 1}, {Category: "code.test", Weight: 1}, {Category: "tool-use", Weight: 1}}},
		{"orchestrator", []Requirement{{Category: "orchestration", Weight: 3}, {Category: "planning", Weight: 2}}},
		{"researcher", []Requirement{{Category: "docs.write", Weight: 1}, {Category: "research", Weight: 3}}},
		{"reviewer", []Requirement{{Category: "review.code", Weight: 3}, {Category: "review.spec", Weight: 1}}}}, []RoleMapping{
		{"fix", []Requirement{{Category: "code.fix", Weight: 3}, {Category: "code.test", Weight: 1}}},
		{"implementation", []Requirement{{Category: "code.implement", Weight: 3}, {Category: "code.test", Weight: 1}}},
		{"ops", []Requirement{{Category: "ops", Weight: 1}}},
		{"research", []Requirement{{Category: "research", Weight: 1}}},
		{"review", []Requirement{{Category: "review.code", Weight: 1}}},
		{"routine", []Requirement{{Category: "routine", Weight: 1}}}}}
}

type RequirementsInput struct {
	SchemaVersion string          `json:"schema_version"`
	Project       string          `json:"project"`
	Role          string          `json:"role"`
	Workload      string          `json:"workload,omitempty"`
	Requirements  []Requirement   `json:"requirements"`
	Mapping       CategoryMapping `json:"mapping"`
}

func RoleRequirements(role, project string) (RequirementsInput, error) {
	m := DefaultMapping()
	for _, r := range m.Roles {
		if r.Role == role {
			return RequirementsInput{SchemaVersion, project, role, "", r.Requirements, m}, nil
		}
	}
	return RequirementsInput{}, refuse("evidence_unknown_role", "no declared mapping for role")
}

type Normalisation struct {
	BenchmarkRef Reference `json:"benchmark_ref"`
	Metric       string    `json:"metric"`
	Min          float64   `json:"min"`
	Max          float64   `json:"max"`
	Direction    string    `json:"direction"`
	Categories   []string  `json:"categories"`
}
type Weight struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}
type Derivation struct {
	SchemaVersion     string          `json:"schema_version"`
	Name              string          `json:"name"`
	Version           string          `json:"version"`
	Normalisations    []Normalisation `json:"normalisations"`
	ConfidenceWeights []Weight        `json:"confidence_weights"`
	BasisWeights      []Weight        `json:"basis_weights"`
	PartialDiscount   float64         `json:"partial_discount"`
	StrongThreshold   float64         `json:"strong_threshold"`
	AdequateThreshold float64         `json:"adequate_threshold"`
	StaleNoteWeight   float64         `json:"stale_note_weight"`
}

// TODO(decision): v1 uses relative utility on [-1,1], confidence/basis priors,
// a 0.5 partial discount, and thresholds 0.2/0.4. Missing categories do not
// create zero-valued observations. Only the named benchmark mapping is pooled.
func DefaultDerivation() Derivation {
	return Derivation{SchemaVersion, "role-suitability", "1", []Normalisation{{Reference{"bug-hunt-bench", "2026-09-13"}, "fixed", 0, 105, "higher_better", []string{"code.fix"}}}, []Weight{{"high", 1}, {"low", 0.25}, {"medium", 0.6}}, []Weight{{"benchmark-reading", 0.4}, {"incident", 0.8}, {"operator-judgement", 0.6}, {"review-outcomes", 1}, {"run-outcomes", 0.8}}, 0.5, 0.4, 0.2, 0}
}

type DerivationRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}
type ViewInputs struct {
	SchemaVersion          string `json:"schema_version"`
	EvidenceSnapshotDigest string `json:"evidence_snapshot_digest"`
	DerivationDigest       string `json:"derivation_digest"`
	RoleRequirementsDigest string `json:"role_requirements_digest"`
	WeightsPolicyDigest    string `json:"weights_policy_digest"`
	EvaluatedAt            string `json:"evaluated_at"`
	CandidateSetDigest     string `json:"candidate_set_digest"`
}
type Coverage struct {
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Match    string `json:"match"`
}
type Contribution struct {
	Ref       string  `json:"ref"`
	Category  string  `json:"category"`
	Direction string  `json:"direction"`
	Weight    float64 `json:"weight"`
	Stale     bool    `json:"stale"`
	AgeS      *int64  `json:"age_s,omitempty"`
}
type Suitability struct {
	Role          string         `json:"role"`
	Candidate     Fingerprint    `json:"candidate"`
	Grade         string         `json:"grade"`
	Score         *float64       `json:"score,omitempty"`
	Scale         Scale          `json:"scale"`
	Coverage      []Coverage     `json:"coverage"`
	Contributions []Contribution `json:"contributions"`
	Derivation    DerivationRef  `json:"derivation"`
}
type View struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	Inputs        ViewInputs        `json:"inputs"`
	Requirements  RequirementsInput `json:"requirements"`
	Derivation    Derivation        `json:"derivation"`
	Candidates    []Fingerprint     `json:"candidates"`
	Results       []Suitability     `json:"results"`
	Issues        []Issue           `json:"issues"`
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func (r RequirementsInput) Validate() error {
	if r.SchemaVersion != SchemaVersion || !known(r.Role) || !known(r.Project) || r.Requirements == nil || r.Mapping.SchemaVersion != SchemaVersion || r.Mapping.Name == "" || r.Mapping.Version == "" || r.Mapping.Roles == nil || r.Mapping.Workloads == nil {
		return refuse("evidence_invalid_requirements", "invalid role requirements or mapping")
	}
	check := func(items []Requirement) error {
		if err := canonical.CheckOrdered(items, func(r Requirement) string { return r.Category }); err != nil {
			return err
		}
		for _, x := range items {
			if !known(x.Category) || !finite(x.Weight) || x.Weight <= 0 {
				return refuse("evidence_invalid_requirements", "category weights must be positive and finite")
			}
			if err := facetsOK(x.Facets); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(r.Requirements); err != nil {
		return err
	}
	for _, table := range [][]RoleMapping{r.Mapping.Roles, r.Mapping.Workloads} {
		if err := canonical.CheckOrdered(table, func(x RoleMapping) string { return x.Role }); err != nil {
			return err
		}
		for _, x := range table {
			if !known(x.Role) || x.Requirements == nil {
				return refuse("evidence_invalid_requirements", "invalid mapping row")
			}
			if err := check(x.Requirements); err != nil {
				return err
			}
		}
	}
	return nil
}
func (d Derivation) Validate() error {
	if d.SchemaVersion != SchemaVersion || d.Name == "" || d.Version == "" || d.Normalisations == nil || d.ConfidenceWeights == nil || d.BasisWeights == nil || !finite(d.PartialDiscount) || d.PartialDiscount < 0 || d.PartialDiscount > 1 || !finite(d.StrongThreshold) || !finite(d.AdequateThreshold) || d.AdequateThreshold >= d.StrongThreshold || d.StrongThreshold > 1 || d.AdequateThreshold < -1 || d.StaleNoteWeight != 0 {
		return refuse("evidence_invalid_derivation", "invalid derivation parameters")
	}
	if err := canonical.CheckOrdered(d.Normalisations, func(n Normalisation) string { return n.BenchmarkRef.Address() + ":" + n.Metric }); err != nil {
		return err
	}
	for _, n := range d.Normalisations {
		if !finite(n.Min) || !finite(n.Max) || n.Max <= n.Min || !member(n.Direction, "higher_better", "lower_better") || len(n.Categories) == 0 {
			return refuse("evidence_invalid_derivation", "invalid normalisation")
		}
		if err := stringSet(n.Categories); err != nil {
			return err
		}
	}
	for _, list := range [][]Weight{d.ConfidenceWeights, d.BasisWeights} {
		if err := canonical.CheckOrdered(list, func(w Weight) string { return w.Key }); err != nil {
			return err
		}
		for _, w := range list {
			if !known(w.Key) || !finite(w.Value) || w.Value < 0 || w.Value > 1 {
				return refuse("evidence_invalid_derivation", "invalid note weight")
			}
		}
	}
	for _, c := range []string{"low", "medium", "high"} {
		if !hasWeight(d.ConfidenceWeights, c) {
			return refuse("evidence_invalid_derivation", "missing confidence weight")
		}
	}
	for _, b := range []string{"review-outcomes", "run-outcomes", "operator-judgement", "incident", "benchmark-reading"} {
		if !hasWeight(d.BasisWeights, b) {
			return refuse("evidence_invalid_derivation", "missing basis weight")
		}
	}
	return nil
}
func hasWeight(ws []Weight, key string) bool {
	for _, w := range ws {
		if w.Key == key {
			return true
		}
	}
	return false
}
func weight(ws []Weight, key string) float64 {
	for _, w := range ws {
		if w.Key == key {
			return w.Value
		}
	}
	return 0
}
func fingerprintKey(f Fingerprint) string {
	b, _ := canonical.Marshal(struct {
		SchemaVersion string      `json:"schema_version"`
		Fingerprint   Fingerprint `json:"fingerprint"`
	}{SchemaVersion, f})
	return string(b)
}
func CandidateDigest(c []Fingerprint) (string, error) {
	if c == nil {
		return "", refuse("evidence_invalid_candidates", "candidate list is required")
	}
	for _, f := range c {
		if f.ModelID == "" {
			return "", refuse("evidence_invalid_candidates", "required unknown model is the literal unknown")
		}
	}
	if err := canonical.CheckOrdered(c, fingerprintKey); err != nil {
		return "", err
	}
	return canonical.Digest(struct {
		SchemaVersion string        `json:"schema_version"`
		Candidates    []Fingerprint `json:"candidates"`
	}{SchemaVersion, c})
}

// Match implements the full fingerprint table; unknown members on both sides
// remain partial. A known conflict always rejects, including engine submembers.
func Match(a, b Fingerprint, r Registry) (bool, string) {
	if !r.Resolves(a.ModelID) || !r.Resolves(b.ModelID) || a.ModelID != b.ModelID || !r.Accepts(a.ModelID, a.Effort) || !r.Accepts(b.ModelID, b.Effort) || a.Effort != b.Effort {
		return false, "none"
	}
	partial := false
	compare := func(a, b string) bool {
		if !known(a) || !known(b) {
			partial = true
			return true
		}
		return a == b
	}
	for _, pair := range [][2]string{{a.Runtime, b.Runtime}, {a.HarnessVersion, b.HarnessVersion}, {a.ToolProfile, b.ToolProfile}, {a.ExecutionProfile, b.ExecutionProfile}} {
		if !compare(pair[0], pair[1]) {
			return false, "none"
		}
	}
	if a.EngineProfile == nil || b.EngineProfile == nil {
		partial = true
	} else {
		x, y := a.EngineProfile, b.EngineProfile
		for _, p := range [][2]string{{x.Name, y.Name}, {x.EngineKind, y.EngineKind}, {x.WeightDigest, y.WeightDigest}, {x.Quantization, y.Quantization}} {
			if !compare(p[0], p[1]) {
				return false, "none"
			}
		}
		for _, p := range [][2]*int64{{x.KVContextTokens, y.KVContextTokens}, {x.PrefillChunkTokens, y.PrefillChunkTokens}} {
			if p[0] == nil || p[1] == nil {
				partial = true
			} else if *p[0] != *p[1] {
				return false, "none"
			}
		}
	}
	if partial {
		return true, "partial"
	}
	return true, "direct"
}
func facetsMatch(actual, requested *Facets, role string) (bool, bool) {
	if actual != nil && len(actual.Roles) > 0 && !contains(actual.Roles, role) {
		return false, false
	}
	if requested == nil {
		return true, false
	}
	if actual == nil {
		return true, true
	}
	partial := false
	for _, p := range [][2][]string{{actual.Languages, requested.Languages}, {actual.Platforms, requested.Platforms}} {
		if len(p[1]) == 0 {
			continue
		}
		if len(p[0]) == 0 {
			partial = true
			continue
		}
		for _, x := range p[1] {
			if !contains(p[0], x) {
				return false, false
			}
		}
	}
	for _, p := range [][2]string{{actual.ContextSize, requested.ContextSize}, {actual.Horizon, requested.Horizon}} {
		if known(p[1]) {
			if !known(p[0]) {
				partial = true
			} else if p[0] != p[1] {
				return false, false
			}
		}
	}
	return true, partial
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// TODO(decision): note harness ranges accept exact versions or inclusive [a,b]
// numeric dotted versions. Unsupported ranges remain stored but never match.
func harnessInRange(version, span string) bool {
	if version == span {
		return true
	}
	if !strings.HasPrefix(span, "[") || !strings.HasSuffix(span, "]") {
		return false
	}
	parts := strings.Split(span[1:len(span)-1], ",")
	if len(parts) != 2 {
		return false
	}
	cmp := func(a, b string) (int, bool) {
		aa, bb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
		n := len(aa)
		if len(bb) > n {
			n = len(bb)
		}
		result := 0
		for i := 0; i < n; i++ {
			x, y := 0, 0
			var err error
			if i < len(aa) {
				x, err = strconv.Atoi(aa[i])
				if err != nil {
					return 0, false
				}
			}
			if i < len(bb) {
				y, err = strconv.Atoi(bb[i])
				if err != nil {
					return 0, false
				}
			}
			if result == 0 {
				if x < y {
					result = -1
				}
				if x > y {
					result = 1
				}
			}
		}
		return result, true
	}
	low, ok := cmp(version, strings.TrimSpace(parts[0]))
	if !ok {
		return false
	}
	high, ok := cmp(version, strings.TrimSpace(parts[1]))
	return ok && low >= 0 && high <= 0
}

// TODO(decision): note directness describes its declared subject axes (efforts,
// runtime and harness range); unlike measurements, notes have no engine/tool axes.
func matchNote(n Note, c Fingerprint, r Registry) (bool, string) {
	if !r.Resolves(n.Subject.ModelID) || n.Subject.ModelID != c.ModelID || !r.Accepts(c.ModelID, c.Effort) {
		return false, "none"
	}
	partial := false
	if len(n.Subject.Efforts) == 0 {
		partial = true
	} else if !contains(n.Subject.Efforts, c.Effort) {
		return false, "none"
	}
	if !known(n.Subject.Runtime) || !known(c.Runtime) {
		partial = true
	} else if n.Subject.Runtime != c.Runtime {
		return false, "none"
	}
	if !known(n.Subject.HarnessVersionRange) || !known(c.HarnessVersion) {
		partial = true
	} else if !harnessInRange(c.HarnessVersion, n.Subject.HarnessVersionRange) {
		return false, "none"
	}
	if partial {
		return true, "partial"
	}
	return true, "direct"
}
func viewWeightsDigest(d Derivation) (string, error) {
	return canonical.Digest(struct {
		SchemaVersion string   `json:"schema_version"`
		Confidence    []Weight `json:"confidence"`
		Basis         []Weight `json:"basis"`
		Partial       float64  `json:"partial"`
		Strong        float64  `json:"strong"`
		Adequate      float64  `json:"adequate"`
		Stale         float64  `json:"stale"`
	}{SchemaVersion, d.ConfidenceWeights, d.BasisWeights, d.PartialDiscount, d.StrongThreshold, d.AdequateThreshold, d.StaleNoteWeight})
}

// Derive excludes notes created after evaluatedAt: a frozen view cannot use
// judgements that did not yet exist. Resolution uses the snapshot-bound registry.
func Derive(snapshot Snapshot, imports []Import, r Registry, req RequirementsInput, d Derivation, candidates []Fingerprint, evaluatedAt string) (View, error) {
	view := View{SchemaVersion: SchemaVersion, Requirements: req, Derivation: d, Candidates: candidates, Results: []Suitability{}, Issues: []Issue{}}
	at, err := instant(evaluatedAt)
	if err != nil {
		return view, refuse("evidence_invalid_time", "evaluated_at must be RFC3339")
	}
	if err = r.Validate(); err != nil {
		return view, err
	}
	if err = req.Validate(); err != nil {
		return view, err
	}
	if err = d.Validate(); err != nil {
		return view, err
	}
	sid, err := snapshot.Digest()
	if err != nil {
		return view, err
	}
	rd, err := canonical.Digest(r)
	if err != nil {
		return view, err
	}
	if rd != snapshot.RegistryDigest || snapshot.RegistryRef != r.Reference {
		return view, refuse("evidence_registry_mismatch", "derivation registry differs from snapshot")
	}
	ids := []string{}
	for _, doc := range imports {
		id, err := doc.Digest()
		if err != nil {
			return view, err
		}
		ids = append(ids, id)
	}
	canonical.SortByKey(ids, func(s string) string { return s })
	if len(ids) != len(snapshot.Imports) {
		return view, refuse("evidence_snapshot_mismatch", "import set differs from snapshot")
	}
	for i := range ids {
		if ids[i] != snapshot.Imports[i] {
			return view, refuse("evidence_snapshot_mismatch", "import set differs from snapshot")
		}
	}
	if err = checkBenchmarks(imports); err != nil {
		return view, err
	}
	a, err := ResolveSnapshot(imports, r)
	if err != nil {
		return view, err
	}
	view.Issues = a.Issues
	// Report unmapped active observations once per category, independently of
	// candidate count, so absent normalisations are inspectable even in empty views.
	for _, rec := range a.Records {
		if rec.Status != "active" || rec.Observation == nil {
			continue
		}
		o := rec.Observation
		for _, category := range o.Categories {
			mapped := false
			for _, n := range d.Normalisations {
				if n.BenchmarkRef == o.BenchmarkRef && n.Metric == o.Metric && contains(n.Categories, category) {
					mapped = true
					break
				}
			}
			if !mapped {
				view.Issues = append(view.Issues, Issue{"observation_unmapped", rec.ID, o.BenchmarkRef.Address() + "/" + o.Metric + "/" + category})
			}
		}
	}
	canonical.SortByKey(view.Issues, func(i Issue) string { return i.Code + "\x00" + i.Ref + "\x00" + i.Detail })
	dd, err := canonical.Digest(d)
	if err != nil {
		return view, err
	}
	qd, err := canonical.Digest(req)
	if err != nil {
		return view, err
	}
	wd, err := viewWeightsDigest(d)
	if err != nil {
		return view, err
	}
	cd, err := CandidateDigest(candidates)
	if err != nil {
		return view, err
	}
	view.Inputs = ViewInputs{SchemaVersion, sid, dd, qd, wd, at.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), cd}
	view.ID, err = canonical.Digest(view.Inputs)
	if err != nil {
		return view, err
	}
	ref := DerivationRef{d.Name, d.Version, dd}
	for _, candidate := range candidates {
		s := Suitability{Role: req.Role, Candidate: candidate, Grade: "unknown", Scale: Scale{-1, 1}, Coverage: []Coverage{}, Contributions: []Contribution{}, Derivation: ref}
		coveredWeight := 0.0
		score := 0.0
		for _, need := range req.Requirements {
			coverage := Coverage{need.Category, "none", "none"}
			contrib := []Contribution{}
			seen := map[string]bool{}
			evidenceCount := 0
			// Direct observations precede partial reprints; record ids break ties.
			for _, pass := range []string{"direct", "partial"} {
				for _, rec := range a.Records {
					if rec.Status != "active" || rec.Observation == nil {
						continue
					}
					o := rec.Observation
					if o.SubjectResolution != "resolved" || !contains(o.Categories, need.Category) {
						continue
					}
					ok, match := Match(o.Subject, candidate, r)
					if !ok {
						continue
					}
					ok, fp := facetsMatch(o.Facets, need.Facets, req.Role)
					if !ok {
						continue
					}
					if fp {
						match = "partial"
					}
					if match != pass {
						continue
					}
					var norm *Normalisation
					for i := range d.Normalisations {
						n := &d.Normalisations[i]
						if n.BenchmarkRef == o.BenchmarkRef && n.Metric == o.Metric && contains(n.Categories, need.Category) {
							norm = n
							break
						}
					}
					if norm == nil {
						continue
					}
					origin := o.Provenance.OriginRef + "\x00" + o.BenchmarkRef.Address() + "\x00" + o.Metric
					if !known(o.Provenance.OriginRef) {
						origin = rec.ID
					}
					if seen[origin] {
						continue
					}
					seen[origin] = true
					val := (o.Value - norm.Min) / (norm.Max - norm.Min)
					if norm.Direction == "lower_better" {
						val = 1 - val
					}
					val = math.Max(0, math.Min(1, val))
					if match == "partial" {
						val *= d.PartialDiscount
					}
					contrib = append(contrib, Contribution{Ref: rec.ID, Category: need.Category, Direction: "+", Weight: val})
					evidenceCount++
					coverage = preferCoverage(coverage, Coverage{need.Category, o.EvidenceKind, match})
				}
			}
			for _, rec := range a.Records {
				if rec.Status != "active" || rec.Note == nil {
					continue
				}
				n := rec.Note
				if !contains(n.Claim.Categories, need.Category) {
					continue
				}
				ok, match := matchNote(*n, candidate, r)
				if !ok {
					continue
				}
				ok, fp := facetsMatch(n.Claim.Facets, need.Facets, req.Role)
				if !ok {
					continue
				}
				if fp {
					match = "partial"
				}
				review, _ := reviewTime(n.ReviewBy)
				created, _ := instant(n.CreatedAt)
				if created.After(at) {
					continue
				}
				stale := at.After(review)
				if len(n.ReviewBy) == 10 {
					stale = !at.Before(review.AddDate(0, 0, 1))
				}
				age := int64(at.Sub(created).Seconds())
				val := weight(d.ConfidenceWeights, n.Confidence) * weight(d.BasisWeights, n.Basis)
				if match == "partial" {
					val *= d.PartialDiscount
				}
				if stale {
					val = 0
				}
				direction := "+"
				if n.Claim.Polarity != "strength" {
					direction = "-"
				}
				contrib = append(contrib, Contribution{rec.ID, need.Category, direction, val, stale, &age})
				if !stale && val > 0 {
					evidenceCount++
					coverage = preferCoverage(coverage, Coverage{need.Category, "notes-only", match})
				}
			}
			s.Coverage = append(s.Coverage, coverage)
			if evidenceCount > 0 {
				coveredWeight += need.Weight
			}
			for i := range contrib {
				if evidenceCount > 0 {
					contrib[i].Weight *= need.Weight / float64(evidenceCount)
				}
				s.Contributions = append(s.Contributions, contrib[i])
			}
		}
		if coveredWeight > 0 {
			for i := range s.Contributions {
				x := &s.Contributions[i]
				x.Weight /= coveredWeight
				if x.Direction == "-" {
					score -= x.Weight
				} else {
					score += x.Weight
				}
			}
			s.Score = &score
			s.Grade = grade(score, d)
		}
		canonical.SortByKey(s.Contributions, func(c Contribution) string { return c.Category + ":" + c.Ref })
		view.Results = append(view.Results, s)
	}
	return view, nil
}
func grade(score float64, d Derivation) string {
	if score >= d.StrongThreshold {
		return "strong"
	}
	if score >= d.AdequateThreshold {
		return "adequate"
	}
	return "weak"
}
func preferCoverage(a, b Coverage) Coverage {
	rank := func(c Coverage) int {
		v := 0
		switch c.Kind {
		case "measured":
			v = 8
		case "interpolated":
			v = 6
		case "transferred":
			v = 4
		case "notes-only":
			v = 2
		}
		if c.Match == "direct" {
			v++
		}
		return v
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}
func (v View) Validate() error {
	if v.SchemaVersion != SchemaVersion || v.Inputs.SchemaVersion != SchemaVersion || v.Results == nil || v.Issues == nil {
		return refuse("evidence_invalid_view", "invalid view header")
	}
	if err := v.Requirements.Validate(); err != nil {
		return err
	}
	if err := v.Derivation.Validate(); err != nil {
		return err
	}
	if _, err := instant(v.Inputs.EvaluatedAt); err != nil {
		return refuse("evidence_invalid_time", "invalid view time")
	}
	for _, s := range []string{v.Inputs.EvidenceSnapshotDigest, v.Inputs.DerivationDigest, v.Inputs.RoleRequirementsDigest, v.Inputs.WeightsPolicyDigest, v.Inputs.CandidateSetDigest} {
		if !validDigest(s) {
			return refuse("evidence_invalid_digest", "invalid view input digest")
		}
	}
	dd, err := canonical.Digest(v.Derivation)
	if err != nil {
		return err
	}
	qd, err := canonical.Digest(v.Requirements)
	if err != nil {
		return err
	}
	wd, err := viewWeightsDigest(v.Derivation)
	if err != nil {
		return err
	}
	cd, err := CandidateDigest(v.Candidates)
	if err != nil {
		return err
	}
	id, err := canonical.Digest(v.Inputs)
	if err != nil {
		return err
	}
	if id != v.ID || dd != v.Inputs.DerivationDigest || qd != v.Inputs.RoleRequirementsDigest || wd != v.Inputs.WeightsPolicyDigest || cd != v.Inputs.CandidateSetDigest {
		return refuse("evidence_view_mismatch", "view inputs do not bind identity")
	}
	if len(v.Results) != len(v.Candidates) {
		return refuse("evidence_invalid_view", "results must cover all candidates")
	}
	for i, s := range v.Results {
		if fingerprintKey(s.Candidate) != fingerprintKey(v.Candidates[i]) || s.Role != v.Requirements.Role || s.Derivation != (DerivationRef{v.Derivation.Name, v.Derivation.Version, dd}) || s.Scale != (Scale{-1, 1}) || s.Contributions == nil || len(s.Coverage) != len(v.Requirements.Requirements) {
			return refuse("evidence_invalid_view", "result shape or candidate mismatch")
		}
		total := 0.0
		for _, c := range s.Contributions {
			if !validAddress(c.Ref) || !finite(c.Weight) || c.Weight < 0 || !member(c.Direction, "+", "-") || c.Stale && c.Weight != 0 {
				return refuse("evidence_invalid_view", "invalid contribution")
			}
			if c.Direction == "-" {
				total -= c.Weight
			} else {
				total += c.Weight
			}
		}
		for j, c := range s.Coverage {
			if c.Category != v.Requirements.Requirements[j].Category || !member(c.Kind, "measured", "interpolated", "transferred", "notes-only", "none") || !member(c.Match, "direct", "partial", "none") {
				return refuse("evidence_invalid_view", "invalid coverage")
			}
		}
		if s.Score == nil {
			if s.Grade != "unknown" || total != 0 {
				return refuse("evidence_invalid_view", "unknown score contradicts contributions")
			}
		} else if !finite(*s.Score) || math.Abs(total-*s.Score) > 1e-12 || s.Grade != grade(*s.Score, v.Derivation) {
			return refuse("evidence_invalid_view", fmt.Sprintf("contributions disagree with score for result %d", i))
		}
	}
	return nil
}

// EvidenceCandidates provides advisory inspection fingerprints, never admission.
// TODO(decision): use exact observation subjects and note scopes; unscoped note
// efforts enumerate the frozen vocabulary. With no usable subjects, enumerate
// registry model/effort pairs so an empty store yields unknown grades.
func EvidenceCandidates(imports []Import, r Registry) []Fingerprint {
	found := map[string]Fingerprint{}
	for _, doc := range imports {
		for _, o := range doc.Observations {
			if o.SubjectResolution == "resolved" && r.Accepts(o.Subject.ModelID, o.Subject.Effort) {
				found[fingerprintKey(o.Subject)] = o.Subject
			}
		}
		for _, n := range doc.Notes {
			efforts := n.Subject.Efforts
			if len(efforts) == 0 {
				for _, m := range r.Models {
					if m.ModelID == n.Subject.ModelID {
						efforts = m.Efforts
						break
					}
				}
			}
			for _, e := range efforts {
				if r.Accepts(n.Subject.ModelID, e) {
					f := Fingerprint{ModelID: n.Subject.ModelID, Effort: e, Runtime: n.Subject.Runtime}
					found[fingerprintKey(f)] = f
				}
			}
		}
	}
	if len(found) == 0 {
		for _, m := range r.Models {
			for _, e := range m.Efforts {
				f := Fingerprint{ModelID: m.ModelID, Effort: e}
				found[fingerprintKey(f)] = f
			}
		}
	}
	out := []Fingerprint{}
	for _, f := range found {
		out = append(out, f)
	}
	canonical.SortByKey(out, fingerprintKey)
	return out
}
