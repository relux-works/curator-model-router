package recommend

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func localFixture(t testing.TB) (Request, LocalCapabilityDocument) {
	t.Helper()
	raw, err := os.ReadFile("testdata/fictional-local-capability.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := LoadLocalCapability(raw)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := FreezeLocalCapability(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := Candidate{Runtime: "local", Model: "fictional", Effort: "none", WeightsID: doc.Materializations[0].WeightsID, ExpectedWeightsID: doc.Materializations[0].WeightsID, Reasoning: &ReasoningContext{Thinking: "on", Effort: "high"}}
	return Request{SchemaVersion: LocalRequestVersion, LocalCapability: &bundle, Catalog: Catalog{SchemaVersion: CatalogVersion, Rows: []CatalogRow{{Candidate: c, Family: "fictional", Billing: "local", Quality: Quality{}, Cost: Cost{Kind: "estimate", Source: "FICTIONAL", AsOf: "2026-10-09"}}}}, Candidates: []Candidate{c}, Task: TaskProfile{Role: "developer", TaskClass: "code.implement", Difficulty: "hard"}, Policy: DefaultPolicy(), Usage: UsageSnapshot{AsOf: 1800000000}}, doc
}
func setLocalDocument(t testing.TB, r *Request, doc LocalCapabilityDocument) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := FreezeLocalCapability(raw)
	if err != nil {
		t.Fatal(err)
	}
	r.LocalCapability = &bundle
}
func TestWeightsIdentityGrammar(t *testing.T) {
	good := "gguf-sha256:" + strings.Repeat("a", 64)
	if err := ValidateWeightsID(good); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", " " + good, good + "\n", strings.ToUpper(good), "sha256:" + strings.Repeat("a", 64), "gguf-split-sha256:" + strings.Repeat("a", 64), "gguf-sha256:" + strings.Repeat("a", 63), "gguf-sha256:" + strings.Repeat("z", 64)} {
		if ValidateWeightsID(id) == nil {
			t.Fatal("accepted", id)
		}
	}
	r, _ := localFixture(t)
	r.Candidates[0].WeightsID = "malformed"
	if _, err := BuildDecision(r); err == nil {
		t.Fatal("malformed candidate accepted")
	}
	r, _ = localFixture(t)
	r.Candidates[0].ExpectedWeightsID = "malformed"
	if _, err := BuildDecision(r); err == nil {
		t.Fatal("malformed pin accepted")
	}
}
func TestLocalTransferAndMeasuredOverride(t *testing.T) {
	r, doc := localFixture(t)
	d, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.SelectorVersion != LocalSelectorVersion || d.SchemaVersion != LocalDecisionVersion || d.Recommendation.Selected == nil {
		t.Fatal(d)
	}
	x := d.Recommendation.Explanation[0]
	if x.Tier != TierS || x.Quality.Value != 76 || *x.Quality.Stderr != 5 || x.LocalRating.SelectionValue.number() != 66 || x.Quality.N != nil || x.LocalRating.Path != "base_quantization_transfer" {
		t.Fatal(x)
	}
	// A measured score lower than the transfer wins, without a transfer penalty.
	doc.Materializations[0].Measurements = []LocalMeasurement{{ID: "lower-exact-measurement", Axis: "coding", Reasoning: *r.Candidates[0].Reasoning, Scope: "code.implement", Score: LocalScore{Value: "40", Stderr: "9", Kind: "measured", Source: "FICTIONAL exact measurement", AsOf: "2026-10-09"}}}
	setLocalDocument(t, &r, doc)
	r.Task.Difficulty = "trivial"
	d, err = BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	x = d.Recommendation.Explanation[0]
	if x.Quality.Value != 40 || x.LocalRating.Path != "exact_weights_measured" || x.LocalRating.SelectionValue.number() != 40 || !slices.Contains(x.ReasonCodes, "exact_weights_measured") {
		t.Fatal(x)
	}
	// Incompatible measurements cannot hide the applicable transfer.
	doc.Materializations[0].Measurements[0].Reasoning.Thinking = "off"
	setLocalDocument(t, &r, doc)
	d, err = BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Explanation[0].Quality.Value != 76 {
		t.Fatal(d)
	}
}
func TestLocalUnratedAndLocks(t *testing.T) {
	edits := []struct {
		name   string
		edit   func(*Request)
		reason string
	}{
		{"missing weights", func(r *Request) { r.Catalog.Rows[0].WeightsID = ""; r.Candidates[0].WeightsID = "" }, "weights_identity_missing"},
		{"missing pin", func(r *Request) { r.Candidates[0].ExpectedWeightsID = "" }, "weights_pin_missing"},
		{"wrong pin", func(r *Request) { r.Candidates[0].ExpectedWeightsID = "gguf-sha256:" + strings.Repeat("3", 64) }, "weights_identity_mismatch"},
		{"repinned alias", func(r *Request) {
			r.Candidates[0].WeightsID = "gguf-sha256:" + strings.Repeat("3", 64)
			r.Candidates[0].ExpectedWeightsID = r.Candidates[0].WeightsID
		}, "weights_identity_mismatch"},
		{"different weights", func(r *Request) {
			r.Catalog.Rows[0].WeightsID = ""
			r.Candidates[0].WeightsID = "gguf-sha256:" + strings.Repeat("3", 64)
			r.Candidates[0].ExpectedWeightsID = r.Candidates[0].WeightsID
		}, "weights_unrated"},
		{"off", func(r *Request) {
			r.Catalog.Rows[0].Reasoning = nil
			r.Candidates[0].Reasoning = &ReasoningContext{Thinking: "off", Effort: "high"}
		}, "quality_context_mismatch"},
		{"unknown thinking", func(r *Request) {
			r.Catalog.Rows[0].Reasoning = nil
			r.Candidates[0].Reasoning = &ReasoningContext{Thinking: "unknown", Effort: "high"}
		}, "quality_context_mismatch"},
		{"missing context", func(r *Request) { r.Catalog.Rows[0].Reasoning = nil; r.Candidates[0].Reasoning = nil }, "quality_context_mismatch"},
		{"context guard", func(r *Request) { r.Candidates[0].Reasoning = &ReasoningContext{Thinking: "on", Effort: "low"} }, "quality_context_mismatch"},
		{"effort mismatch", func(r *Request) {
			r.Catalog.Rows[0].Reasoning = nil
			r.Candidates[0].Reasoning = &ReasoningContext{Thinking: "on", Effort: "low"}
		}, "quality_context_mismatch"},
		{"no evidence", func(r *Request) { r.LocalCapability = nil }, "weights_unrated"},
	}
	for _, tc := range edits {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := localFixture(t)
			tc.edit(&r)
			for _, lock := range []Locks{{}, {Agent: "local"}, {Agent: "local", Model: "fictional"}, {Agent: "local", Model: "fictional", Effort: "none"}} {
				r.Locks = lock
				d, err := BuildDecision(r)
				if err != nil {
					t.Fatal(err)
				}
				x := d.Recommendation.Explanation[0]
				if x.Quality != nil || x.Tier != TierU || !slices.Contains(x.ReasonCodes, tc.reason) {
					t.Fatal(x)
				}
				explicit := lock.Effort != ""
				if (d.Recommendation.Selected != nil) != explicit || x.Qualified != explicit {
					t.Fatal("lock handling", d)
				}
				if explicit && !slices.Contains(x.ReasonCodes, "explicit_lock_unrated") {
					t.Fatal(x)
				}
			}
			r.Task.Pipeline = "fanout"
			d, err := BuildDecision(r)
			if err != nil {
				t.Fatal(err)
			}
			if d.Recommendation.Selected != nil {
				t.Fatal("unrated fanout", d)
			}
		})
	}
	// An explicit lock cannot restore policy-forbidden or unadmitted candidates.
	r, _ := localFixture(t)
	r.LocalCapability = nil
	r.Locks = Locks{Agent: "local", Model: "fictional", Effort: "none"}
	r.Policy.Rules = []Rule{{ID: "forbid-local", Source: "fictional policy", Forbid: &RuleSelector{Runtime: "local"}}}
	d, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Selected != nil {
		t.Fatal(d)
	}
	r.Policy.Rules = nil
	r.Candidates = nil
	d, err = BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Selected != nil {
		t.Fatal(d)
	}
}
func TestLocalExpiryAndUncertaintyRanking(t *testing.T) {
	r, doc := localFixture(t)
	expiry, _ := time.Parse(time.RFC3339, doc.Coefficients.Records[0].ExpiresAt)
	r.Usage.AsOf = expiry.Unix()
	d, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Selected != nil {
		t.Fatal("expired transfer ranked", d)
	}
	r.Usage.AsOf = expiry.Unix() - 1
	d, err = BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Selected == nil {
		t.Fatal("premature expiry", d)
	}
	doc.Coefficients.Records[0].AddedStderr = "20"
	setLocalDocument(t, &r, doc)
	d, err = BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Selected != nil || d.Recommendation.Explanation[0].Tier != TierC {
		t.Fatal("tier used unpenalized score", d)
	}
	doc.Coefficients.Records[0].AddedStderr = "100"
	setLocalDocument(t, &r, doc)
	d, err = BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Explanation[0].LocalRating.SelectionValue.number() != 0 {
		t.Fatal(d)
	}
	// A lower-mean, smaller-uncertainty transfer ranks above a higher mean.
	r, doc = localFixture(t)
	m := doc.Materializations[0]
	m.WeightsID = "gguf-sha256:" + strings.Repeat("4", 64)
	doc.Materializations = append(doc.Materializations, m)
	co := doc.Coefficients.Records[0]
	co.ID = "second-transfer"
	co.WeightsID = m.WeightsID
	co.K = "0.9"
	co.AddedStderr = "0"
	doc.Coefficients.Records = append(doc.Coefficients.Records, co)
	row := r.Catalog.Rows[0]
	row.Model = "less-uncertainty"
	row.WeightsID = m.WeightsID
	row.ExpectedWeightsID = m.WeightsID
	r.Catalog.Rows = append(r.Catalog.Rows, row)
	r.Candidates = append(r.Candidates, row.Candidate)
	setLocalDocument(t, &r, doc)
	d, err = BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Selected == nil || d.Recommendation.Selected.Model != "less-uncertainty" {
		t.Fatal(d)
	}
}
func TestExactDecimalTransfer(t *testing.T) {
	_, doc := localFixture(t)
	co := doc.Coefficients.Records[0]
	co.K = "0.333333"
	co.AddedStderr = "0.000001"
	score, selection, err := transfer(LocalScore{Value: "99.999999", Stderr: "0.000001"}, co)
	if err != nil || score.Value != "33.333299" || score.Stderr != "0.000002" || selection != "33.333295" {
		t.Fatal(score, selection, err)
	}
	for _, raw := range []string{"-0", "1e-1", "0.0000001", "\"0.95\"", "NaN", "null", "-1"} {
		var d Decimal
		if json.Unmarshal([]byte(raw), &d) == nil {
			t.Fatal("decimal accepted", raw)
		}
	}
}
func TestLocalDocumentStrictness(t *testing.T) {
	raw, err := os.ReadFile("testdata/fictional-local-capability.json")
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(string) string{
		func(s string) string { return strings.Replace(s, `"local-capability-v1"`, `"local-capability-v2"`, 1) },
		func(s string) string { return strings.Replace(s, `"bases":`, `"extra":true,"bases":`, 1) },
		func(s string) string { return strings.Replace(s, `"format": "gguf"`, `"format": "mlx"`, 1) },
		func(s string) string { return strings.Replace(s, `"k": 0.95`, `"k": 1.01`, 1) },
		func(s string) string { return strings.Replace(s, `"k": 0.95`, `"k": 9.5e-1`, 1) },
		func(s string) string { return strings.Replace(s, `"k": 0.95,`, ``, 1) },
		func(s string) string { return strings.Replace(s, `"thinking": "on"`, `"thinking": "maybe"`, 1) },
		func(s string) string { return strings.Replace(s, `"thinking": "on",`, ``, 1) },
		func(s string) string { return strings.Replace(s, `"stderr": 2,`, ``, 1) },
		func(s string) string { return strings.Replace(s, `"bases": [`, `"bases":null,"duplicate_bases": [`, 1) },
		func(s string) string {
			return strings.Replace(s, `"id": "fictional-base"`, `"id": "fictional-base", "id":"duplicate"`, 1)
		},
		func(s string) string { return s + `{}` },
		func(s string) string {
			return strings.Replace(s, `"calibration":`, `"key":"forbidden","calibration":`, 1)
		},
	}
	for i, edit := range mutations {
		if _, err := LoadLocalCapability([]byte(edit(string(raw)))); err == nil {
			t.Fatal("accepted mutation", i)
		}
	}
	r, doc := localFixture(t)
	invalid := []func(*LocalCapabilityDocument){
		func(d *LocalCapabilityDocument) {
			d.Coefficients.Records[0].CoefficientInterval = []Decimal{"0.96", "1"}
		},
		func(d *LocalCapabilityDocument) {
			d.Coefficients.Records[0].CoefficientInterval = []Decimal{"0", "1.1"}
		},
		func(d *LocalCapabilityDocument) { d.Coefficients.Records[0].CoefficientInterval = []Decimal{"0"} },
		func(d *LocalCapabilityDocument) { d.Coefficients.Records[0].ExpiresAt = "2030-02-30T00:00:00Z" },
		func(d *LocalCapabilityDocument) { d.Coefficients.Records[0].ExpiresAt = "2030-01-01T00:00:00+00:00" },
		func(d *LocalCapabilityDocument) { d.Coefficients.Records[0].ExpiresAt = "2030-01-01T00:00:00.1Z" },
		func(d *LocalCapabilityDocument) { d.Coefficients.Records[0].TargetReasoning.Effort = "low" },
		func(d *LocalCapabilityDocument) { d.Coefficients.Records[0].Quantization = "wrong" },
		func(d *LocalCapabilityDocument) { d.Coefficients.Records[0].Axis = "review" },
		func(d *LocalCapabilityDocument) {
			d.Materializations = append(d.Materializations, d.Materializations[0])
		},
		func(d *LocalCapabilityDocument) { d.Bases[0].Provenance = map[string]BaseProvenance{} },
		func(d *LocalCapabilityDocument) { d.SourcePriority = []string{"missing"} },
		func(d *LocalCapabilityDocument) {
			d.Bases = append(d.Bases, d.Bases[0])
			d.Bases[1].ID = "second-base"
			d.Bases[1].BaseID = "hf://models/example/Fictional-27B"
		},
	}
	for i, edit := range invalid {
		raw, _ := json.Marshal(doc)
		copy, err := LoadLocalCapability(raw)
		if err != nil {
			t.Fatal(err)
		}
		edit(&copy)
		if copy.Validate() == nil {
			t.Fatal("accepted invalid record", i)
		}
	}
	r.Candidates = append(r.Candidates, r.Candidates[0])
	r.Candidates[1].WeightsID = "gguf-sha256:" + strings.Repeat("4", 64)
	if _, err := BuildDecision(r); err == nil {
		t.Fatal("conflicting tuple admitted")
	}
}
func TestLocalReplayAndOpaqueBytes(t *testing.T) {
	r, _ := localFixture(t)
	d, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := Replay(d); err != nil {
		t.Fatal(err)
	}
	raw, err := d.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDecision(raw); err != nil {
		t.Fatal(err)
	}
	again, err := BuildDecision(r)
	if err != nil || d.DecisionID != again.DecisionID {
		t.Fatal("nondeterminism", err)
	}
	r.Candidates[0].Reasoning = &ReasoningContext{Thinking: "on", Effort: "low"}
	changed, err := BuildDecision(r)
	if err != nil || d.DecisionID == changed.DecisionID {
		t.Fatal("context not bound", err)
	}
	r = d.Inputs
	r.LocalCapability = &LocalCapabilityBundle{ID: r.LocalCapability.ID, JSON: r.LocalCapability.JSON + "\n"}
	if _, err := BuildDecision(r); err == nil {
		t.Fatal("opaque digest ignored")
	}
	newBundle, err := FreezeLocalCapability([]byte(r.LocalCapability.JSON))
	if err != nil {
		t.Fatal(err)
	}
	r.LocalCapability = &newBundle
	changed, err = BuildDecision(r)
	if err != nil || changed.DecisionID == d.DecisionID {
		t.Fatal("exact bytes not bound", err)
	}
	r = d.Inputs
	r.SchemaVersion = ""
	if _, err := BuildDecision(r); err == nil {
		t.Fatal("mixed request accepted")
	}
	d.SelectorVersion = SelectorVersion
	id, _ := d.ContentID()
	d.DecisionID = id
	d.Recommendation.DecisionID = id
	if Replay(d) == nil {
		t.Fatal("mixed selector accepted")
	}
}
func TestLocalMeasurementOnlyReviewAndPriority(t *testing.T) {
	r, doc := localFixture(t)
	doc.Bases = []BaseModelRecord{}
	doc.Coefficients.Records = []QuantizationCoefficient{}
	doc.Materializations[0].BaseRecordID = ""
	measurement := LocalMeasurement{ID: "measured-review", Axis: "review", Reasoning: *r.Candidates[0].Reasoning, Scope: "review.code", Score: LocalScore{Value: "42", Stderr: "2", N: ptr(10), Kind: "measured", Source: "FICTIONAL exact review", AsOf: "2026-10-09"}}
	doc.Materializations[0].Measurements = []LocalMeasurement{measurement}
	r.Task = TaskProfile{Role: "reviewer", TaskClass: "review.code", Difficulty: "hard"}
	setLocalDocument(t, &r, doc)
	d, err := BuildDecision(r)
	if err != nil || d.Recommendation.Selected == nil {
		t.Fatal(d, err)
	}
	doc.Materializations[0].Measurements[0].Score.N = nil
	if doc.Validate() == nil {
		t.Fatal("review missing n")
	}
	doc.Materializations[0].Measurements[0] = measurement
	second := measurement
	second.ID = "other-review"
	second.Score.Value = "80"
	doc.Materializations[0].Measurements = append(doc.Materializations[0].Measurements, second)
	setLocalDocument(t, &r, doc)
	if _, err := BuildDecision(r); err == nil {
		t.Fatal("unresolved tie accepted")
	}
	doc.SourcePriority = []string{measurement.ID, second.ID}
	setLocalDocument(t, &r, doc)
	d, err = BuildDecision(r)
	if err != nil || d.Recommendation.Explanation[0].Quality.Value != 42 {
		t.Fatal("priority picked maximum", d, err)
	}
}
func TestLocalOverlayCannotBypassIdentity(t *testing.T) {
	r, _ := localFixture(t)
	raw := []byte(`{"schema_version":"overlay-v1","rows":{"local":{"fictional":{"none":{"cost":{"usd_per_task":{"value":0,"kind":"estimate","source":"FICTIONAL","as_of":"2026-10-09"}}}}}}}`)
	o, err := LoadOverlay(raw)
	if err != nil {
		t.Fatal("v1 load changed", err)
	}
	if _, err := MergeCatalog(r.Catalog, o); err == nil {
		t.Fatal("local cost-only overlay bypass")
	}
	r.CatalogOverlay = &o
	if _, err := BuildDecision(r); err == nil {
		t.Fatal("unguarded local overlay accepted")
	}
}

// Fails on any accidental use of the default HTTP transport. Provider/library
// APIs additionally expose only bytes, with no URL, client or network discovery.
type failLocalTransport struct{ t *testing.T }

func (f failLocalTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("network attempted")
	return nil, nil
}
func TestBaseProvidersOfflineRegistry(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = failLocalTransport{t}
	defer func() { http.DefaultTransport = previous }()
	registry := DefaultBaseProviders()
	infos := registry.List()
	if len(infos) != 1 || infos[0].NeedsKey || infos[0].Terms == "" {
		t.Fatal(infos)
	}
	if registry.Register(PublicJSONProvider{}) == nil {
		t.Fatal("duplicate registered")
	}
	if registry.Register(nil) == nil {
		t.Fatal("nil registered")
	}
	raw, err := os.ReadFile("testdata/fictional-public-base-export.json")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Import("public-json", raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Import("public-json", raw)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("provider nondeterminism", err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseRecords(encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Import("unknown", raw); err == nil {
		t.Fatal("unknown provider")
	}
	if _, err := registry.Import("public-json", []byte(`{"schema_version":"public-base-export-v1","records":[],"key":"secret"}`)); err == nil {
		t.Fatal("secret/unknown field accepted")
	}
	if _, err := registry.Import("public-json", []byte(`{"schema_version":"wrong","records":[]}`)); err == nil {
		t.Fatal("wrong export version")
	}
	r, _ := localFixture(t)
	if _, err := BuildDecision(r); err != nil {
		t.Fatal(err)
	}
}

func TestLocalCandidateFileStrictness(t *testing.T) {
	r, _ := localFixture(t)
	raw, err := json.Marshal(r.Candidates)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCandidates(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`[{"runtime":"local","model":"fictional","effort":"none","weights_id":""}]`,
		`[{"runtime":"local","model":"fictional","effort":"none","expected_weights_id":""}]`,
		`[{"runtime":"local","model":"fictional","effort":"none","reasoning":{"thinking":"on","effort":" High"}}]`,
		`[{"runtime":"local","model":"fictional","effort":"none","reasoning":{"thinking":"on","effort":"high","extra":true}}]`,
		`[{"runtime":"local","runtime":"other","model":"fictional","effort":"none"}]`,
		`null`, `[] []`,
	} {
		if _, err := LoadCandidates([]byte(bad)); err == nil {
			t.Fatal("invalid candidate file", bad)
		}
	}
	r.Catalog.Rows[0].Billing = "subscription"
	if r.Catalog.Validate() == nil {
		t.Fatal("hosted local identity allowed")
	}
}
func TestHistoricalLocalReplayIsSeparate(t *testing.T) {
	r := fixture(t)
	restrict(&r, "local")
	legacy, err := buildDecision(r, true)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Recommendation.Selected == nil || legacy.SchemaVersion != DecisionVersion {
		t.Fatal(legacy)
	}
	if err := Replay(legacy); err != nil {
		t.Fatal(err)
	}
	modern, err := BuildDecision(legacy.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	if modern.Recommendation.Selected != nil || modern.SchemaVersion != LocalDecisionVersion || modern.DecisionID == legacy.DecisionID {
		t.Fatal("legacy local rating reused", modern)
	}
}

func TestOperatorEstimatePrecedenceAndPinIdentity(t *testing.T) {
	r, doc := localFixture(t)
	co := doc.Coefficients.Records[0]
	co.ID = "operator-transfer"
	co.Method = "operator-estimate"
	co.K = "0.8"
	doc.Coefficients.Records = append(doc.Coefficients.Records, co)
	setLocalDocument(t, &r, doc)
	r.Task.Difficulty = "trivial"
	d, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Explanation[0].LocalRating.EvidenceID != co.ID || d.Recommendation.Explanation[0].Quality.Value != 64 {
		t.Fatal("operator estimate did not take precedence", d)
	}
	r.Catalog.Rows[0].WeightsID = ""
	r.Candidates[0].WeightsID = "gguf-sha256:" + strings.Repeat("9", 64)
	r.Candidates[0].ExpectedWeightsID = r.Candidates[0].WeightsID
	changed, err := BuildDecision(r)
	if err != nil || changed.DecisionID == d.DecisionID {
		t.Fatal("pin/weights not bound", err)
	}
}

func TestLocalReplayRejectsRehashedExplanation(t *testing.T) {
	r, _ := localFixture(t)
	d, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	d.Recommendation.Explanation[0].LocalRating.SelectionValue = "76"
	id, err := d.ContentID()
	if err != nil {
		t.Fatal(err)
	}
	d.DecisionID = id
	d.Recommendation.DecisionID = id
	if Replay(d) == nil {
		t.Fatal("substituted transfer explanation accepted")
	}
}
