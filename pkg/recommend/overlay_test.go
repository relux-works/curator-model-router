package recommend

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func fictionalOverlay(t testing.TB) CatalogOverlay {
	t.Helper()
	raw, err := os.ReadFile("testdata/fictional-overlay.json")
	if err != nil {
		t.Fatal(err)
	}
	o, err := LoadOverlay(raw)
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func TestOverlayPrecedenceDigestReplay(t *testing.T) {
	in := realRequest(t)
	in.Task.Difficulty = "critical"
	base, err := BuildDecision(in)
	if err != nil {
		t.Fatal(err)
	}
	o := fictionalOverlay(t)
	in.CatalogOverlay = &o
	d, err := BuildDecision(in)
	if err != nil {
		t.Fatal(err)
	}
	if d.Recommendation.Selected == nil || *d.Recommendation.Selected != (Candidate{Runtime: "codex", Model: "gpt-6-luna", Effort: "low"}) {
		t.Fatal(d.Recommendation.RenderHuman())
	}
	digest, err := o.Digest()
	if err != nil || d.Inputs.OverlayDigest != digest || d.DecisionID == base.DecisionID {
		t.Fatal("overlay not bound", err)
	}
	var row CatalogRow
	for _, r := range d.Inputs.Catalog.Rows {
		if r.Candidate == *d.Recommendation.Selected {
			row = r
		}
	}
	if *row.Cost.USDPerTask != 0.001 || *row.Cost.TokensPerTask != 12 || row.Cost.USDProvenance.Source == row.Cost.TokensProvenance.Source || row.Cost.TokensProvenance.AsOf != "2026-10-06" {
		t.Fatal("scalar provenance lost", row)
	}
	if row.Quality.Review == nil || row.Quality.Coding.Value != 91 {
		t.Fatal("field merge lost base or overlay", row)
	}
	raw, err := d.JSON()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDecision(raw)
	if err != nil || !reflect.DeepEqual(d, loaded) {
		t.Fatal("replay failed", err)
	}
	reordered := in
	slices.Reverse(reordered.Catalog.Rows)
	slices.Reverse(reordered.Candidates)
	again, err := BuildDecision(reordered)
	if err != nil || again.DecisionID != d.DecisionID {
		t.Fatal("order-dependent", err)
	}
	// Digest is canonical rather than file formatting dependent.
	pretty, _ := json.MarshalIndent(o, "", "  ")
	parsed, err := LoadOverlay(pretty)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := parsed.Digest()
	if same != digest {
		t.Fatal("format changed digest")
	}
	patch := o.Rows["codex"]["gpt-6-luna"]["low"]
	patch.Quality.Coding.Source += " revised fictional provenance"
	changed, err := BuildDecision(in)
	if err != nil || changed.DecisionID == d.DecisionID {
		t.Fatal("provenance not bound", err)
	}
	// Tampering is refused even when the outer record is correctly rehashed.
	d.Inputs.OverlayDigest = "sha256:" + strings.Repeat("0", 64)
	id, err := d.ContentID()
	if err != nil {
		t.Fatal(err)
	}
	d.DecisionID = id
	d.Recommendation.DecisionID = id
	if Replay(d) == nil {
		t.Fatal("substituted overlay digest accepted")
	}
}
func TestOverlayStrictValidationAndUnknownRows(t *testing.T) {
	prefix := `{"schema_version":"overlay-v1","rows":{"r":{"m":{"e":`
	suffix := `}}}}`
	for _, raw := range []string{
		`{}`, `{"schema_version":"overlay-v2","rows":{}}`, `{"schema_version":"overlay-v1","rows":{}}`,
		prefix + `{}` + suffix,
		prefix + `{"billing":"local"}` + suffix,
		prefix + `{"quality":{"coding":{"value":101,"kind":"estimate","source":"FICTIONAL","as_of":"2026-10-07"}}}` + suffix,
		prefix + `{"quality":{"coding":{"value":1,"kind":"estimate","as_of":"2026-10-07"}}}` + suffix,
		prefix + `{"quality":{"review":{"value":1,"kind":"estimate","source":"FICTIONAL","as_of":"2026-10-07"}}}` + suffix,
		prefix + `{"cost":{"usd_per_task":{"kind":"estimate","source":"FICTIONAL","as_of":"2026-10-07"}}}` + suffix,
		prefix + `{"cost":{"usd_per_task":{"value":-1,"kind":"estimate","source":"FICTIONAL","as_of":"2026-10-07"}}}` + suffix,
		prefix + `{"cost":{"tokens_per_task":{"value":1.5,"kind":"estimate","source":"FICTIONAL","as_of":"2026-10-07"}}}` + suffix,
		prefix + `{"cost":{"tokens_per_task":{"value":1,"kind":"estimate","source":"FICTIONAL","as_of":"bad"}}}` + suffix,
		prefix + `{"quality":{"coding":null}}` + suffix,
		`{"schema_version":"overlay-v1","schema_version":"overlay-v1","rows":{}}`,
		prefix + `{"Quality":{}}` + suffix,
	} {
		if _, err := LoadOverlay([]byte(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	in := realRequest(t)
	original, _ := json.Marshal(in.Catalog)
	o := fictionalOverlay(t)
	merged, err := MergeCatalog(in.Catalog, o)
	if err != nil {
		t.Fatal(err)
	}
	for i := range merged.Rows {
		if merged.Rows[i].Candidate == (Candidate{Runtime: "codex", Model: "gpt-6-luna", Effort: "low"}) {
			merged.Rows[i].Quality.Coding.Value = 7
			*merged.Rows[i].Cost.USDPerTask = 7
			merged.Rows[i].Cost.USDProvenance.Source = "mutated copy"
		}
	}
	patch := o.Rows["codex"]["gpt-6-luna"]["low"]
	if patch.Quality.Coding.Value != 91 || patch.Cost.USDPerTask.Value != 0.001 || patch.Cost.USDPerTask.Source == "mutated copy" {
		t.Fatal("merge aliases overlay")
	}
	after, _ := json.Marshal(in.Catalog)
	if string(after) != string(original) {
		t.Fatal("mutated base")
	}
	o.Rows["unknown-runtime"] = o.Rows["codex"]
	if _, err := MergeCatalog(in.Catalog, o); err == nil {
		t.Fatal("unknown row accepted")
	}
	in.OverlayDigest = "sha256:" + strings.Repeat("0", 64)
	if _, err := BuildDecision(in); err == nil {
		t.Fatal("digest without overlay")
	}
}
func TestPrimaryAndBugHuntFallbackNeverMixScales(t *testing.T) {
	for _, class := range []string{"code.implement", "code.fix", "code.refactor", "code.test", "tool-use", "ops", "docs.write", "research", "planning", "orchestration", "routine"} {
		for _, budget := range []string{"economy", "balanced", "burn"} {
			for _, difficulty := range []string{"trivial", "routine", "standard", "hard", "critical"} {
				in := realRequest(t)
				in.Task = TaskProfile{Role: "developer", TaskClass: class, Difficulty: difficulty}
				in.Policy.BudgetMode = budget
				o := fictionalOverlay(t)
				in.CatalogOverlay = &o
				// Make the primary far more expensive than the measured fallback. Group
				// precedence must hold for cheapest, quality, standard and burn objectives.
				patch := o.Rows["codex"]["gpt-6-luna"]["low"]
				patch.Cost.USDPerTask.Value = 10000
				patch.Quality.Coding.Value = 63
				patch.Quality.Overall.Value = 63
				for i := range in.Catalog.Rows {
					if in.Catalog.Rows[i].Model == "claude-sonnet-5-5" && in.Catalog.Rows[i].Effort == "max" {
						in.Catalog.Rows[i].Quality.Review = reviewValue(99, 1, 3)
						in.Catalog.Rows[i].Quality.Review.Source = "FICTIONAL TEST DATA: larger mean on a different scale"
					}
				}
				in.Policy.Rules = []Rule{{ID: "fictional-preference", Source: "FICTIONAL test", Prefer: &RulePreference{Runtimes: []string{"claude", "codex"}}}}
				got := run(t, in)
				if got.Selected == nil || *got.Selected != (Candidate{Runtime: "codex", Model: "gpt-6-luna", Effort: "low"}) {
					t.Fatalf("%s/%s/%s: %s", class, budget, difficulty, got.RenderHuman())
				}
				for _, x := range got.Explanation {
					if x.QualityIndex == "bughunt_fallback" && !slices.Contains(x.ReasonCodes, "quality_from_bughunt_fallback") {
						t.Fatal(x)
					}
				}
			}
		}
	}
	in := realRequest(t)
	in.Task.TaskClass = "research"
	in.Candidates = []Candidate{{Runtime: "muse", Model: "muse-spark-1.3-contributor", Effort: "max"}}
	if got := run(t, in); got.Selected != nil || got.Refusal == nil {
		t.Fatal("unknown quality selected")
	}
}

func TestTaskSourceAndScaleSpecificTiering(t *testing.T) {
	row := CatalogRow{Quality: Quality{Overall: reviewValue(77, 1, 3), Coding: reviewValue(63, 1, 3), Review: reviewValue(41, 1, 3)}}
	for class, index := range map[string]string{"code.implement": "coding", "tool-use": "coding", "ops": "coding", "review.code": "review", "review.spec": "review", "research": "overall", "routine": "overall"} {
		if got := qualityIndex(row, class); got != index {
			t.Fatal(class, got)
		}
	}
	row.Quality.Coding = nil
	if qualityIndex(row, "code.implement") != "bughunt_fallback" {
		t.Fatal("overall substituted for coding")
	}
	row.Quality.Overall = nil
	if qualityIndex(row, "research") != "bughunt_fallback" {
		t.Fatal("coding substituted for overall")
	}
	in := realRequest(t)
	in.Candidates = []Candidate{{Runtime: "codex", Model: "gpt-6-astra", Effort: "max"}}
	for _, class := range []string{"code.implement", "research", "review.code"} {
		in.Task.TaskClass = class
		in.Task.Difficulty = "critical"
		x := chosenExplanation(t, run(t, in))
		if x.Tier != TierS {
			t.Fatal("measured edges not used", x)
		}
	}
	in.Policy.ReviewTierEdges.S = 50
	if got := run(t, in); got.Selected != nil || got.Refusal == nil {
		t.Fatal("measured edge override ignored")
	}
}

func TestOverlayReplacesMeasurementAndPreservesExplicitZero(t *testing.T) {
	in := realRequest(t)
	raw := []byte(`{"schema_version":"overlay-v1","rows":{"codex":{"gpt-6-luna":{"low":{"quality":{"review":{"value":0,"kind":"measured","source":"FICTIONAL TEST: invented zero measurement","as_of":"2026-10-07","stderr":0,"n":1}},"cost":{"usd_per_task":{"value":0,"kind":"estimate","source":"FICTIONAL TEST: invented zero cost","as_of":"2026-10-07"},"tokens_per_task":{"value":0,"kind":"estimate","source":"FICTIONAL TEST: invented zero tokens","as_of":"2026-10-07"}}}}}}}`)
	o, err := LoadOverlay(raw)
	if err != nil {
		t.Fatal(err)
	}
	in.CatalogOverlay = &o
	in.Candidates = []Candidate{{Runtime: "codex", Model: "gpt-6-luna", Effort: "low"}}
	in.Task = TaskProfile{Role: "reviewer", TaskClass: "review.code", Difficulty: "trivial"}
	d, err := BuildDecision(in)
	if err != nil {
		t.Fatal(err)
	}
	x := chosenExplanation(t, d.Recommendation)
	if x.Quality.Value != 0 || x.Quality.Source != "FICTIONAL TEST: invented zero measurement" || x.Cost.USDPerTask == nil || *x.Cost.USDPerTask != 0 || x.Cost.TokensPerTask == nil || *x.Cost.TokensPerTask != 0 {
		t.Fatal("zero treated as absence", x)
	}
	if err = Replay(d); err != nil {
		t.Fatal(err)
	}
}
