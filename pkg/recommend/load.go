package recommend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

func DefaultPolicy() Policy {
	h := routing.DefaultPolicy("v1").Headroom
	h.Enabled = true
	return Policy{SchemaVersion: PolicyVersion, Mode: routing.ModeSelect, TierEdges: TierEdges{62, 56, 45}, ReviewTierEdges: TierEdges{40, 30, 20},
		Difficulties: map[string]DifficultyRule{
			"trivial": {TierC, "cheapest"}, "routine": {TierB, "cheapest"},
			"standard": {TierB, "standard"}, "hard": {TierA, "quality"},
			"critical": {TierS, "quality_ignore_cost"},
		}, StandardAMaxCostRatio: 1.5, InterchangeableCostBP: 2500, FanOutK: 3, Headroom: h}
}

// LoadCatalog rejects duplicate, unknown (including case variants), null, and
// malformed fields. It accepts unordered rows and returns key-sorted rows.
func LoadCatalog(raw []byte) (Catalog, error) {
	var c Catalog
	if err := strict(raw, &c, InvalidCatalog); err != nil {
		return c, err
	}
	c = normalizeCatalog(c)
	return c, c.Validate()
}

// LoadPolicy overlays JSON on built-in defaults; nil bytes mean missing policy.
// Explicit false, zero and empty values are preserved and validated.
func LoadPolicy(raw []byte) (Policy, error) {
	p := DefaultPolicy()
	if len(bytes.TrimSpace(raw)) == 0 {
		return p, nil
	}
	if err := strict(raw, &p, InvalidPolicy); err != nil {
		return Policy{}, err
	}
	// encoding/json replaces map values rather than merging partial structs.
	// Overlay each difficulty separately so a floor-only override keeps its objective.
	var overrides struct {
		Difficulties map[string]json.RawMessage `json:"difficulties"`
	}
	if err := json.Unmarshal(raw, &overrides); err != nil {
		return Policy{}, refuse(InvalidPolicy, err.Error())
	}
	defaults := DefaultPolicy()
	for name, rawRule := range overrides.Difficulties {
		rule := defaults.Difficulties[name]
		if err := json.Unmarshal(rawRule, &rule); err != nil {
			return Policy{}, refuse(InvalidPolicy, err.Error())
		}
		p.Difficulties[name] = rule
	}
	p.Headroom.ProtectedRoles = sortedSet(p.Headroom.ProtectedRoles)
	if !p.Headroom.Windows.All {
		p.Headroom.Windows.IDs = sortedSet(p.Headroom.Windows.IDs)
	}
	return p, p.Validate()
}
func strict(raw []byte, dst any, code string) error {
	// The wrapper lets policy omit schema_version while retaining all canonical
	// safeguards (duplicate keys, nulls, Unicode and trailing documents).
	wrapped := append([]byte(`{"schema_version":"strict-v1","payload":`), raw...)
	wrapped = append(wrapped, '}')
	if _, err := canonical.Canonicalize(wrapped); err != nil {
		return refuse(code, err.Error())
	}
	var obj any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return refuse(code, err.Error())
	}
	if _, ok := obj.(map[string]any); !ok {
		return refuse(code, "root must be an object")
	}
	if err := exactKeys(obj, reflect.TypeOf(dst).Elem(), "$"); err != nil {
		return refuse(code, err.Error())
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return refuse(code, err.Error())
	}
	return nil
}
func fieldTypes(t reflect.Type) map[string]reflect.Type {
	m := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			for k, v := range fieldTypes(f.Type) {
				m[k] = v
			}
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			m[tag] = f.Type
		}
	}
	return m
}
func exactKeys(v any, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	// WindowFilter is a custom string-or-list wire value.
	if t == reflect.TypeFor[routing.WindowFilter]() {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		fields := fieldTypes(t)
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			ft, ok := fields[k]
			if !ok {
				return fmt.Errorf("%s.%s is unknown", path, k)
			}
			if err := exactKeys(obj[k], ft, path+"."+k); err != nil {
				return err
			}
		}

		if t == reflect.TypeFor[Candidate]() || t == reflect.TypeFor[CatalogRow]() {
			for _, key := range []string{"weights_id", "expected_weights_id"} {
				if value, present := obj[key]; present {
					id, ok := value.(string)
					if !ok || ValidateWeightsID(id) != nil {
						return fmt.Errorf("%s.%s is malformed", path, key)
					}
				}
			}
		}

		required := map[reflect.Type][]string{
			reflect.TypeFor[ReasoningContext]():        {"thinking", "effort"},
			reflect.TypeFor[LocalScore]():              {"value", "stderr", "kind", "source", "as_of"},
			reflect.TypeFor[QuantizationCoefficient](): {"id", "weights_id", "base_record_id", "quantization", "axis", "scope", "source_reasoning", "target_reasoning", "method", "k", "coefficient_interval", "added_stderr", "source", "rationale", "as_of", "expires_at"},
		}
		for _, key := range required[t] {
			if _, ok := obj[key]; !ok {
				return fmt.Errorf("%s.%s is required", path, key)
			}
		}
		if t == reflect.TypeFor[QualityValue]() || t == reflect.TypeFor[OverlayNumber]() || t == reflect.TypeFor[OverlayTokens]() {
			if _, ok := obj["value"]; !ok {
				return fmt.Errorf("%s.value is required", path)
			}
		}
	case reflect.Map:
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if err := exactKeys(obj[k], t.Elem(), path+"."+k); err != nil {
				return err
			}
		}
	case reflect.Slice:
		items, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		for i, item := range items {
			if err := exactKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c Catalog) Validate() error {
	if c.SchemaVersion != CatalogVersion {
		return refuse(InvalidCatalog, "schema_version must be catalog-v1")
	}
	if c.Rows == nil {
		return refuse(InvalidCatalog, "rows is required; use [] for an empty catalog")
	}
	seen := map[string]bool{}
	for _, r := range c.Rows {
		if !validCandidate(r.Candidate) || r.Family == "" {
			return refuse(InvalidCatalog, "row requires runtime, model, effort and family")
		}
		if seen[r.Key()] {
			return refuse(InvalidCatalog, "duplicate configuration "+r.Key())
		}
		seen[r.Key()] = true
		if r.Billing != routing.BillingSubscription && r.Billing != routing.BillingLocal && r.Billing != routing.BillingMetered {
			return refuse(InvalidCatalog, "unknown billing")
		}
		if r.Billing != routing.BillingLocal && (r.WeightsID != "" || r.ExpectedWeightsID != "" || r.Reasoning != nil) {
			return refuse(InvalidCatalog, "hosted row cannot carry local identity")
		}
		if r.Latency != "" && r.Latency != "fast" && r.Latency != "medium" && r.Latency != "slow" {
			return refuse(InvalidCatalog, "unknown latency")
		}
		for _, q := range []*QualityValue{r.Quality.Overall, r.Quality.Coding, r.Quality.Review} {
			if q == nil {
				continue
			}
			if !finite(q.Value) || q.Value < 0 || q.Value > 100 || !provenance(q.Kind, q.Source, q.AsOf) || (q.Stderr != nil && (!finite(*q.Stderr) || *q.Stderr < 0)) || (q.N != nil && *q.N < 1) {
				return refuse(InvalidCatalog, "invalid quality or provenance")
			}
		}
		if q := r.Quality.Review; q != nil && (q.Kind != "measured" || q.Stderr == nil || q.N == nil) {
			return refuse(InvalidCatalog, "review requires measured quality, stderr and n")
		}
		cost := r.Cost
		for _, v := range []*ValueProvenance{cost.USDProvenance, cost.TokensProvenance} {
			if v != nil && !validProvenance(*v) {
				return refuse(InvalidCatalog, "invalid scalar cost provenance")
			}
		}
		if (cost.USDProvenance != nil && cost.USDPerTask == nil) || (cost.TokensProvenance != nil && cost.TokensPerTask == nil) {
			return refuse(InvalidCatalog, "cost provenance without value")
		}
		if !provenance(cost.Kind, cost.Source, cost.AsOf) || (cost.USDPerTask != nil && (!finite(*cost.USDPerTask) || *cost.USDPerTask < 0)) || (cost.TokensPerTask != nil && *cost.TokensPerTask < 0) {
			return refuse(InvalidCatalog, "invalid cost or provenance")
		}
		for _, f := range r.Constraints.NotFor {
			if f == "" {
				return refuse(InvalidCatalog, "not_for contains an empty facet")
			}
		}
	}
	if _, err := canonical.Marshal(c); err != nil {
		return refuse(InvalidCatalog, err.Error())
	}
	return nil
}
func (p Policy) Validate() error {
	if p.PreflightTimeoutSeconds < 0 || p.PreflightTimeoutSeconds > 86400 {
		return refuse(InvalidPolicy, "preflight_timeout_seconds must be between 0 and 86400")
	}
	if p.SchemaVersion != PolicyVersion {
		return refuse(InvalidPolicy, "unknown policy schema_version")
	}
	if !slices.Contains([]string{"", "economy", "balanced", "burn"}, p.BudgetMode) {
		return refuse(InvalidPolicy, "budget_mode must be economy, balanced or burn")
	}
	if p.BurnFanOutCap < 0 || p.BurnFanOutCap > 100 {
		return refuse(InvalidPolicy, "burn_fanout_cap must be 1..100 when set")
	}
	if p.Mode != routing.ModeSelect && p.Mode != routing.ModeRecommend && p.Mode != routing.ModeShadow {
		return refuse(InvalidPolicy, "mode must be select, recommend or shadow")
	}
	for _, e := range []TierEdges{p.TierEdges, p.ReviewTierEdges} {
		if !finite(e.S) || !finite(e.A) || !finite(e.B) || e.S > 100 || e.B < 0 || e.S <= e.A || e.A <= e.B {
			return refuse(InvalidPolicy, "tier edges must satisfy 0 <= B < A < S <= 100")
		}
	}
	if len(p.Difficulties) != 5 {
		return refuse(InvalidPolicy, "all five difficulty rules are required")
	}
	for _, name := range []string{"trivial", "routine", "standard", "hard", "critical"} {
		r, ok := p.Difficulties[name]
		if !ok || tierRank(r.MinimumTier) < 1 {
			return refuse(InvalidPolicy, "invalid minimum tier for "+name)
		}
		if r.Objective != "cheapest" && r.Objective != "standard" && r.Objective != "quality" && r.Objective != "quality_ignore_cost" {
			return refuse(InvalidPolicy, "unknown objective for "+name)
		}
	}
	if !finite(p.StandardAMaxCostRatio) || p.StandardAMaxCostRatio < 1 || p.InterchangeableCostBP < 0 || p.InterchangeableCostBP > 10000 || p.FanOutK < 1 || p.FanOutK > 100 {
		return refuse(InvalidPolicy, "invalid ratio, cost band or fanout_k")
	}
	if err := validateRules(p.Rules); err != nil {
		return err
	}
	// Recommend computes disjoint groups from tiers and costs, so caller groups
	// cannot replace that contract. Other headroom knobs reuse routing validation.
	if p.Headroom.Equivalence != routing.SamePairAnyHome || len(p.Headroom.Groups) != 0 {
		return refuse(InvalidPolicy, "recommend derives headroom groups from tier and cost")
	}
	rp := routing.DefaultPolicy("v1")
	rp.Headroom = p.Headroom
	if err := rp.Validate(); err != nil {
		return refuse(InvalidPolicy, err.Error())
	}
	if _, err := canonical.Marshal(p); err != nil {
		return refuse(InvalidPolicy, err.Error())
	}
	return nil
}
func validTaskClass(class string) bool {
	return slices.Contains([]string{"code.implement", "code.fix", "code.refactor", "code.test", "review.code", "review.spec", "docs.write", "research", "planning", "orchestration", "tool-use", "ops", "routine"}, class)
}

// MapTaskClass translates board and workload vocabulary without guessing task content.
// Use explicit review.spec when board-content context is unavailable; operations
// uses the known orchestrator role when supplied.
func MapTaskClass(class string, roles ...string) string {
	switch class {
	case "implementation", "code", "unified":
		return "code.implement"
	case "debugging":
		return "code.fix"
	case "testing":
		return "code.test"
	case "migration":
		return "code.refactor"
	case "review":
		return "review.code"
	case "documentation", "docs":
		return "docs.write"
	case "architecture":
		return "planning"
	case "mechanical", "metadata":
		return "routine"
	case "operations":
		if len(roles) != 0 && roles[0] == "orchestrator" {
			return "orchestration"
		}
		return "ops"
	default:
		return class
	}
}
func (t TaskProfile) Normalize() (TaskProfile, error) {
	if t.Role == "" {
		return t, refuse(InvalidTask, "role is required")
	}
	original := t.TaskClass
	t.TaskClass = MapTaskClass(original, t.Role)
	if !validTaskClass(t.TaskClass) {
		return t, refuse(InvalidTask, fmt.Sprintf("unknown class %q", original))
	}
	if t.OriginalTaskClass == "" && original != t.TaskClass {
		t.OriginalTaskClass = original
	}
	if t.OriginalTaskClass != "" && MapTaskClass(t.OriginalTaskClass, t.Role) != t.TaskClass {
		return t, refuse(InvalidTask, "original task class does not match mapped class")
	}
	if t.Difficulty == "" {
		switch t.TaskClass {
		case "routine", "docs.write":
			t.Difficulty = "routine"
		case "research", "planning", "orchestration":
			t.Difficulty = "hard"
		default:
			t.Difficulty = "standard"
		}
	}
	if !slices.Contains([]string{"trivial", "routine", "standard", "hard", "critical"}, t.Difficulty) {
		return t, refuse(InvalidTask, "unknown difficulty")
	}
	if t.Sensitivity == "" {
		t.Sensitivity = "normal"
	}
	if t.Sensitivity != "normal" && t.Sensitivity != "delicate" {
		return t, refuse(InvalidTask, "unknown sensitivity")
	}
	if t.Pipeline == "" {
		t.Pipeline = "single"
	}
	if t.Pipeline != "single" && t.Pipeline != "fanout" {
		return t, refuse(InvalidTask, "unknown pipeline")
	}
	return t, nil
}
func TierFor(q *QualityValue, p Policy) Tier {
	if q == nil {
		return TierU
	}
	v := q.Value
	if q.Stderr != nil {
		v -= *q.Stderr
	}
	switch {
	case v >= p.TierEdges.S:
		return TierS
	case v >= p.TierEdges.A:
		return TierA
	case v >= p.TierEdges.B:
		return TierB
	default:
		return TierC
	}
}
func tierRank(t Tier) int { return map[Tier]int{TierU: 0, TierC: 1, TierB: 2, TierA: 3, TierS: 4}[t] }
func validCandidate(c Candidate) bool {
	return c.Runtime != "" && c.Model != "" && c.Effort != "" &&
		(c.WeightsID == "" || ValidateWeightsID(c.WeightsID) == nil) &&
		(c.ExpectedWeightsID == "" || ValidateWeightsID(c.ExpectedWeightsID) == nil) &&
		(c.Reasoning == nil || validReasoning(*c.Reasoning))
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validText(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return utf8.ValidString(v.String())
	case reflect.Pointer:
		return v.IsNil() || validText(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !validText(v.Field(i)) {
				return false
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if !validText(v.Index(i)) {
				return false
			}
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			if !validText(key) || !validText(v.MapIndex(key)) {
				return false
			}
		}
	}
	return true
}
func provenance(kind, source, asOf string) bool {
	if kind != "measured" && kind != "vendor" && kind != "estimate" {
		return false
	}
	if source == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339, asOf)
	if err == nil {
		return true
	}
	_, err = time.Parse("2006-01-02", asOf)
	return err == nil
}
func sortedSet(v []string) []string { v = slices.Clone(v); slices.Sort(v); return slices.Compact(v) }
func normalizeCatalog(c Catalog) Catalog {
	c.Rows = slices.Clone(c.Rows)
	for i := range c.Rows {
		c.Rows[i].Constraints.NotFor = sortedSet(c.Rows[i].Constraints.NotFor)
	}
	slices.SortFunc(c.Rows, func(a, b CatalogRow) int { return strings.Compare(a.Key(), b.Key()) })
	return c
}
