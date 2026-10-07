package recommend

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
)

func reviewValue(v, se float64, n int) *QualityValue {
	return &QualityValue{Value: v, Kind: "measured", Source: "synthetic-review-fixture@v1", AsOf: "2026-10-01", Stderr: ptr(se), N: ptr(n)}
}

func TestReviewIndicesCannotReplaceMissingMeasurement(t *testing.T) {
	for _, class := range []string{"review.code", "review.spec"} {
		in := fixture(t)
		restrict(&in, "alpha", "super")
		in.Task = TaskProfile{Role: "reviewer", TaskClass: class, Difficulty: "hard"}
		for i := range in.Catalog.Rows {
			if in.Catalog.Rows[i].Model == "alpha" {
				in.Catalog.Rows[i].Quality.Review = reviewValue(50, 1, 3)
			}
		}
		got := run(t, in)
		selected(t, got, "alpha")
		x := explanation(t, got, "super")
		if x.Quality != nil || x.Tier != TierU || x.Qualified {
			t.Fatal("index substituted for review", x)
		}
		in.Candidates = slices.DeleteFunc(in.Candidates, func(c Candidate) bool { return c.Model == "alpha" })
		if got := run(t, in); got.Refusal == nil || got.Selected != nil {
			t.Fatal("unknown review selected")
		}
	}
}
func TestReviewQualityOverridesIndices(t *testing.T) {
	for _, class := range []string{"review.code", "review.spec"} {
		r := fixture(t)
		restrict(&r, "alpha", "super")
		r.Task.TaskClass = class
		r.Task.Difficulty = "hard"
		for i := range r.Catalog.Rows {
			row := &r.Catalog.Rows[i]
			if row.Model == "alpha" {
				row.Quality.Review = reviewValue(50, 1, 3)
			}
			if row.Model == "super" {
				row.Quality.Review = reviewValue(35, 1, 3)
			}
		}
		got := run(t, r)
		selected(t, got, "alpha")
		x := explanation(t, got, "alpha")
		if x.Tier != TierS || x.QualityIndex != "review" || x.Quality.Value != 50 || slices.Contains(x.ReasonCodes, "review_quality_from_index") {
			t.Fatal(x)
		}
		// Non-review ranking and tiers keep using the original indices.
		r.Task.TaskClass = "code.implement"
		selected(t, run(t, r), "super")
	}
}
func TestShippedMuseQualityUnknown(t *testing.T) {
	c, err := LoadCatalog(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	measured := 0
	var muse CatalogRow
	for _, row := range c.Rows {
		if row.Quality.Review != nil {
			measured++
		}
		if row.Runtime == "muse" {
			muse = row
		}
	}
	if measured != 37 || muse.Runtime == "" || muse.Quality != (Quality{}) {
		t.Fatal("invalid public measurement coverage")
	}
	for _, class := range []string{"review.code", "review.spec", "code.implement", "research"} {
		r := Request{Catalog: c, Candidates: []Candidate{muse.Candidate}, Policy: DefaultPolicy(), Task: TaskProfile{Role: "reviewer", TaskClass: class, Difficulty: "trivial"}, Usage: UsageSnapshot{AsOf: 1800000000}}
		got := run(t, r)
		if got.Refusal == nil || got.Selected != nil {
			t.Fatal("Muse without quality selected")
		}
		x := explanation(t, got, muse.Model)
		if x.Tier != TierU || x.QualityIndex != "unknown" || !slices.Contains(x.ReasonCodes, "quality_unknown") {
			t.Fatal(x)
		}
	}
}
func TestReviewNoiseTies(t *testing.T) {
	for _, difficulty := range []string{"hard", "critical"} {
		for _, tc := range []struct {
			name                string
			mean, se, otherCost float64
			want                string
		}{
			{"larger stderr and cheaper", 51, 3, 1, "alpha"},
			{"outside noise", 54, 3, 1, "super"},
			{"strict boundary", 53, 3, 1, "super"},
			{"equal mean zero noise", 50, 0, 1, "alpha"},
			{"cost then key", 50, 2, 2, "alpha"},
		} {
			t.Run(difficulty+"/"+tc.name, func(t *testing.T) {
				r := fixture(t)
				restrict(&r, "alpha", "super")
				r.Task.TaskClass = "review.code"
				r.Task.Difficulty = difficulty
				for i := range r.Catalog.Rows {
					row := &r.Catalog.Rows[i]
					if row.Model == "alpha" {
						row.Quality.Review = reviewValue(50, 0, 3)
						row.Cost.USDPerTask = ptr(tc.otherCost)
					}
					if row.Model == "super" {
						row.Quality.Review = reviewValue(tc.mean, tc.se, 3)
						row.Cost.USDPerTask = ptr(2.0)
					}
				}
				got := run(t, r)
				selected(t, got, tc.want)
				// More headroom cannot reverse the prescribed cost/key quality tie.
				quota(&r, "super", 1000, 300, 0, "fresh")
				quota(&r, "alpha", 3000, 9000, 0, "fresh")
				selected(t, run(t, r), tc.want)
				slices.Reverse(r.Candidates)
				slices.Reverse(r.Catalog.Rows)
				replay, err := BuildDecision(r)
				if err != nil {
					t.Fatal(err)
				}
				if err = Replay(replay); err != nil {
					t.Fatal(err)
				}
				selected(t, replay.Recommendation, tc.want)
			})
		}
	}
	// A-B and B-C ties do not chain into a wider A-C band.
	r := fixture(t)
	restrict(&r, "alpha", "delta", "super")
	r.Task.TaskClass = "review.code"
	r.Task.Difficulty = "hard"
	for i := range r.Catalog.Rows {
		row := &r.Catalog.Rows[i]
		switch row.Model {
		case "super":
			row.Quality.Review = reviewValue(52, 1.5, 3)
			row.Cost.USDPerTask = ptr(3.0)
		case "alpha":
			row.Quality.Review = reviewValue(51, 1.5, 3)
			row.Cost.USDPerTask = ptr(2.0)
		case "delta":
			row.Quality.Review = reviewValue(50, 1.5, 3)
			row.Cost.USDPerTask = ptr(1.0)
		}
	}
	selected(t, run(t, r), "alpha")
}
func TestReviewEdgesAndValidation(t *testing.T) {
	p, err := LoadPolicy([]byte(`{"review_tier_edges":{"a":32}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.ReviewTierEdges != (TierEdges{40, 32, 20}) || p.TierEdges != DefaultPolicy().TierEdges {
		t.Fatal(p)
	}
	_, err = LoadPolicy([]byte(`{"review_tier_edges":{"a":40}}`))
	expectCode(t, err, InvalidPolicy)
	raw, err := os.ReadFile("testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = LoadCatalog(raw); err != nil {
		t.Fatal("legacy catalog-v1 failed", err)
	}
	r := fixture(t)
	r.Catalog.Rows[0].Quality.Review = reviewValue(50, 2, 1)
	for _, change := range []func(*QualityValue){
		func(q *QualityValue) { q.N = ptr(0) }, func(q *QualityValue) { q.N = nil },
		func(q *QualityValue) { q.Stderr = nil }, func(q *QualityValue) { q.Kind = "estimate" },
	} {
		c := r.Catalog
		row := c.Rows[0]
		q := *row.Quality.Review
		change(&q)
		row.Quality.Review = &q
		c.Rows = slices.Clone(c.Rows)
		c.Rows[0] = row
		expectCode(t, c.Validate(), InvalidCatalog)
	}
}

// These legacy golden picks retain the original unscoped host reviewer pin. Scoped
// v1.1 rules are tested separately in budget_test.go.
func TestShippedReviewGoldenPicks(t *testing.T) {
	c, err := LoadCatalog(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	// Retain the unscoped pin as an explicit operator-policy regression.
	var results []struct {
		Difficulty string     `json:"difficulty"`
		Pinned     bool       `json:"mini"` // Legacy golden field: whether the example reviewer pin is enabled.
		Selected   *Candidate `json:"selected,omitempty"`
		Tier       Tier       `json:"tier,omitempty"`
		Index      string     `json:"index,omitempty"`
		Refusal    string     `json:"refusal,omitempty"`
	}
	for _, pinned := range []bool{false, true} {
		for _, difficulty := range []string{"trivial", "routine", "standard", "hard", "critical"} {
			r := Request{Catalog: c, Policy: DefaultPolicy(), Task: TaskProfile{Role: "reviewer", TaskClass: "review.code", Difficulty: difficulty}, Usage: UsageSnapshot{AsOf: 1800000000}}
			for _, row := range c.Rows {
				r.Candidates = append(r.Candidates, row.Candidate)
			}
			if pinned {
				r.Policy.Rules = []Rule{{ID: "host-review-astra-medium", Source: "operator ruling: host reviewer pin (example)", Rationale: "Synthetic mirror of example host reviewer pin.", When: RuleWhen{Role: "reviewer"}, Require: &RuleSelector{Runtime: "codex", Model: "gpt-6-astra"}, Effort: &RuleEffort{Pin: "medium"}}}
			}
			got := run(t, r)
			item := struct {
				Difficulty string     `json:"difficulty"`
				Pinned     bool       `json:"mini"` // Legacy golden field: whether the example reviewer pin is enabled.
				Selected   *Candidate `json:"selected,omitempty"`
				Tier       Tier       `json:"tier,omitempty"`
				Index      string     `json:"index,omitempty"`
				Refusal    string     `json:"refusal,omitempty"`
			}{Difficulty: difficulty, Pinned: pinned, Selected: got.Selected}
			if got.Refusal != nil {
				item.Refusal = got.Refusal.Code
			} else {
				for _, x := range got.Explanation {
					if x.Selected {
						item.Tier = x.Tier
						item.Index = x.QualityIndex
					}
				}
			}
			results = append(results, item)
			d, err := BuildDecision(r)
			if err != nil {
				t.Fatal(err)
			}
			if err = Replay(d); err != nil {
				t.Fatal(err)
			}
			slices.Reverse(r.Catalog.Rows)
			slices.Reverse(r.Candidates)
			shuffled, err := BuildDecision(r)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d, shuffled) {
				t.Fatal("permutation changed decision")
			}
		}
	}
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	path := "testdata/review-picks.golden.json"
	if os.Getenv("EVC_UPDATE_GOLDENS") == "1" {
		if err = os.WriteFile(path, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(want) {
		t.Fatalf("review picks differ: %s", b)
	}
}
