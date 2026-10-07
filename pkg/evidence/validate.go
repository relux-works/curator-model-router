package evidence

import (
	"bytes"
	"encoding/json"
	"github.com/relux-works/curator-model-router/pkg/canonical"
	"math"
	"reflect"
	"strings"
	"time"
)

func member(s string, choices ...string) bool {
	for _, x := range choices {
		if s == x {
			return true
		}
	}
	return false
}
func known(s string) bool                 { return s != "" && s != canonical.Unknown }
func instant(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

// TODO(decision): date-only review_by remains current through its full UTC date;
// RFC3339 review_by is an exact instant. The derivation freezes this boundary.
func reviewTime(s string) (time.Time, error) {
	if len(s) == 10 {
		return time.Parse("2006-01-02", s)
	}
	return instant(s)
}
func required(v reflect.Value, path string) error {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return required(v.Elem(), path)
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			tag := strings.Split(t.Field(i).Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			f := v.Field(i)
			if len(tag) > 1 && tag[1] == "omitempty" && f.IsZero() {
				continue
			}
			if err := required(f, path+"."+tag[0]); err != nil {
				return err
			}
		}
	case reflect.String:
		if v.String() == "" {
			return refuse("evidence_missing_field", path+" is required; use unknown when appropriate")
		}
	case reflect.Slice:
		if v.IsNil() {
			return refuse("evidence_missing_field", path+" must be [] for known-empty")
		}
		for i := 0; i < v.Len(); i++ {
			if err := required(v.Index(i), path); err != nil {
				return err
			}
		}
	}
	return nil
}
func strictKeys(raw json.RawMessage, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return refuse("evidence_invalid_json", "expected object")
		}
		keys := make([]string, 0, len(object))
		for k := range object {
			keys = append(keys, k)
		}
		canonical.SortByKey(keys, func(s string) string { return s })
		for _, k := range keys {
			found := false
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if strings.Split(f.Tag.Get("json"), ",")[0] == k {
					found = true
					if err := strictKeys(object[k], f.Type); err != nil {
						return err
					}
					break
				}
			}
			if !found {
				return refuse("evidence_unknown_field", "unknown field: "+k)
			}
		}
		for i := 0; i < t.NumField(); i++ {
			tag := strings.Split(t.Field(i).Tag.Get("json"), ",")
			if tag[0] == "-" || len(tag) > 1 && tag[1] == "omitempty" {
				continue
			}
			if _, ok := object[tag[0]]; !ok {
				return refuse("evidence_missing_field", "missing field: "+tag[0])
			}
		}
	case reflect.Slice:
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return refuse("evidence_invalid_json", "expected array")
		}
		for _, x := range list {
			if err := strictKeys(x, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Decode refuses nulls, unknown keys (including differently cased keys), duplicate
// keys and trailing data before normalisation can lose any information.
func Decode(raw []byte, out any) error {
	b, err := canonical.Canonicalize(raw)
	if err != nil {
		return err
	}
	t := reflect.TypeOf(out)
	if t.Kind() != reflect.Pointer {
		return refuse("evidence_invalid_json", "decode requires a pointer")
	}
	if err = strictKeys(b, t.Elem()); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return refuse("evidence_invalid_json", err.Error())
	}
	return nil
}
func authorOK(a Author) bool { return member(a.Kind, "human", "agent") && known(a.ID) }
func (v Import) Validate() error {
	for _, o := range v.Observations {
		if o.EvidenceKind == "transferred" && (o.Transfer == nil || !known(o.Transfer.RuleID) || !known(o.Transfer.RuleVersion) || (o.Transfer.From.ModelID == "" && o.Transfer.From.Effort == "") || o.Uncertainty == nil) {
			return refuse("evidence_transfer_required", "transferred values require from, rule_id, rule_version and uncertainty")
		}
	}
	if err := required(reflect.ValueOf(v), "import"); err != nil {
		return err
	}
	if v.SchemaVersion != SchemaVersion || v.Kind != "evidence-import" {
		return refuse("evidence_schema", "unsupported evidence document")
	}
	if !member(v.MeasuredBy, "vendor", "third_party", "internal") {
		return refuse("evidence_unknown_enum", "invalid measured_by")
	}
	if _, err := instant(v.ImportedAt); err != nil {
		return refuse("evidence_invalid_time", "invalid imported_at")
	}
	if err := canonical.CheckOrdered(v.Benchmarks, func(b Benchmark) string { return b.Address() }); err != nil {
		return err
	}
	benches := map[string]Benchmark{}
	for _, b := range v.Benchmarks {
		if !member(b.GradingMethod, "tests", "exact-match", "llm-judge", "human", "mixed") {
			return refuse("evidence_unknown_enum", "invalid benchmark grading_method")
		}
		if b.ProtocolDigest != "" && !validDigest(b.ProtocolDigest) && b.ProtocolDigest != canonical.Unknown {
			return refuse("evidence_invalid_digest", "invalid protocol_digest")
		}
		if err := canonical.CheckOrdered(b.Categories, func(c CategoryCoverage) string { return c.Category }); err != nil {
			return err
		}
		if err := canonical.CheckOrdered(b.Metrics, func(m Metric) string { return m.Name }); err != nil {
			return err
		}
		for _, m := range b.Metrics {
			if !member(m.Direction, "higher_better", "lower_better") {
				return refuse("evidence_unknown_enum", "invalid metric direction")
			}
			if m.Scale != nil && (!(m.Scale.Max > m.Scale.Min) || math.IsInf(m.Scale.Min, 0) || math.IsInf(m.Scale.Max, 0)) {
				return refuse("evidence_invalid_value", "invalid metric scale")
			}
		}
		benches[b.Address()] = b
	}
	for _, o := range v.Observations {
		if !member(o.SubjectResolution, "resolved", "unresolved") || !member(o.EvidenceKind, "measured", "interpolated", "transferred") || !member(o.GradingMethod, "tests", "exact-match", "llm-judge", "human") {
			return refuse("evidence_unknown_enum", "invalid observation enum")
		}
		if o.EvidenceKind == "transferred" && (o.Transfer == nil || !known(o.Transfer.RuleID) || !known(o.Transfer.RuleVersion) || (o.Transfer.From.ModelID == "" && o.Transfer.From.Effort == "") || o.Uncertainty == nil) {
			return refuse("evidence_transfer_required", "transferred values require from, rule_id, rule_version and uncertainty")
		}
		if o.EvidenceKind != "transferred" && o.Transfer != nil {
			return refuse("evidence_transfer_forbidden", "transfer only belongs to transferred values")
		}
		if math.IsNaN(o.Value) || math.IsInf(o.Value, 0) || o.SampleCount != nil && *o.SampleCount < 0 {
			return refuse("evidence_invalid_value", "invalid observation value or count")
		}
		if o.Uncertainty != nil && (!member(o.Uncertainty.Kind, "stderr", "ci95", "range") || o.Uncertainty.Value < 0) {
			return refuse("evidence_invalid_value", "invalid uncertainty")
		}
		if o.Cost != nil && (o.Cost.TokensIn != nil && *o.Cost.TokensIn < 0 || o.Cost.TokensOut != nil && *o.Cost.TokensOut < 0 || o.Cost.USD != nil && *o.Cost.USD < 0 || o.Cost.WallS != nil && *o.Cost.WallS < 0) {
			return refuse("evidence_invalid_value", "negative cost")
		}
		if _, err := instant(o.Provenance.RetrievedAt); err != nil {
			return refuse("evidence_invalid_time", "invalid retrieved_at")
		}
		if o.ObservedAt != canonical.Unknown {
			if _, err := instant(o.ObservedAt); err != nil {
				return refuse("evidence_invalid_time", "invalid observed_at")
			}
		}
		if b, ok := benches[o.BenchmarkRef.Address()]; ok {
			if err := checkObservationBenchmark(o, b); err != nil {
				return err
			}
		}
		if err := stringSet(o.Categories); err != nil {
			return err
		}
		if err := stringSet(o.Supersedes); err != nil {
			return err
		}
		if err := facetsOK(o.Facets); err != nil {
			return err
		}
	}
	for _, n := range v.Notes {
		if !member(n.Claim.Polarity, "strength", "weakness", "caution") || !member(n.Basis, "review-outcomes", "run-outcomes", "operator-judgement", "incident", "benchmark-reading") || !member(n.Confidence, "low", "medium", "high") || !authorOK(n.Author) {
			return refuse("evidence_unknown_enum", "invalid note enum")
		}
		created, err := instant(n.CreatedAt)
		if err != nil {
			return refuse("evidence_invalid_time", "invalid created_at")
		}
		review, err := reviewTime(n.ReviewBy)
		if err != nil || review.Before(created) {
			return refuse("evidence_invalid_time", "review_by precedes created_at or is invalid")
		}
		for _, r := range n.EvidenceRefs {
			if !member(r.Kind, "run", "review", "pull-request", "board-element", "observation", "document") {
				return refuse("evidence_unknown_enum", "invalid evidence reference kind")
			}
		}
		if err := stringSet(n.Claim.Categories); err != nil {
			return err
		}
		if err := stringSet(n.Subject.Efforts); err != nil {
			return err
		}
		if err := stringSet(n.Supersedes); err != nil {
			return err
		}
		if err := facetsOK(n.Claim.Facets); err != nil {
			return err
		}
		if err := canonical.CheckOrdered(n.EvidenceRefs, func(r EvidenceRef) string { return r.Kind + ":" + r.Ref }); err != nil {
			return err
		}
	}
	for _, r := range v.Retractions {
		if !validAddress(r.Target) || strings.HasPrefix(r.Target, "ret:") {
			return refuse("evidence_invalid_target", "retractions target observations or notes")
		}
		if !authorOK(r.Author) {
			return refuse("evidence_unknown_enum", "invalid retraction author")
		}
		if _, err := instant(r.At); err != nil {
			return refuse("evidence_invalid_time", "invalid retraction time")
		}
	}
	for _, o := range v.Observations {
		if err := addressOK("obs:", o.ID, o, o.Supersedes); err != nil {
			return err
		}
	}
	for _, n := range v.Notes {
		if err := addressOK("note:", n.ID, n, n.Supersedes); err != nil {
			return err
		}
	}
	for _, r := range v.Retractions {
		if err := addressOK("ret:", r.ID, r, nil); err != nil {
			return err
		}
	}
	if err := canonical.CheckOrdered(v.Observations, func(o Observation) string { return o.ID }); err != nil {
		return err
	}
	if err := canonical.CheckOrdered(v.Notes, func(n Note) string { return n.ID }); err != nil {
		return err
	}
	if err := canonical.CheckOrdered(v.Retractions, func(r Retraction) string { return r.ID }); err != nil {
		return err
	}
	_, err := canonical.Marshal(v)
	return err
}
func checkObservationBenchmark(o Observation, b Benchmark) error {
	metric := false
	for _, m := range b.Metrics {
		if m.Name == o.Metric {
			metric = true
		}
	}
	if !metric {
		return refuse("evidence_invalid_metric", "metric is not declared by benchmark")
	}
	for _, c := range o.Categories {
		found := false
		for _, bc := range b.Categories {
			if c == bc.Category {
				found = true
			}
		}
		if !found {
			return refuse("evidence_invalid_category", "observation category is not declared by benchmark")
		}
	}
	return nil
}
func stringSet(s []string) error {
	return canonical.CheckOrdered(s, func(x string) string { return x })
}
func facetsOK(f *Facets) error {
	if f == nil {
		return nil
	}
	for _, s := range [][]string{f.Languages, f.Platforms, f.Roles} {
		if err := stringSet(s); err != nil {
			return err
		}
	}
	return nil
}
func addressOK(prefix, id string, v any, edges []string) error {
	expected, err := RecordAddress(prefix, v)
	if err != nil {
		return err
	}
	if expected != id {
		return refuse("evidence_address_mismatch", "record address does not bind content")
	}
	for _, e := range edges {
		if !validAddress(e) || strings.HasPrefix(e, "ret:") {
			return refuse("evidence_invalid_target", "supersedes must target observations or notes")
		}
	}
	return nil
}
