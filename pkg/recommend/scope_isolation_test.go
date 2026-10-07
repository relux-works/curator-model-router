package recommend

import (
	"reflect"
	"slices"
	"testing"
)

func TestNonmatchingScopeCannotChangeReviewRanking(t *testing.T) {
	for _, when := range []RuleWhen{
		{Host: "other", Difficulty: []string{"hard"}},
		{Role: "developer", Difficulty: []string{"hard"}},
		{TaskClass: "review.spec", Difficulty: []string{"hard"}},
		{Story: "other", Difficulty: []string{"hard"}},
		{Difficulty: []string{"trivial"}},
		{Sensitivity: []string{"delicate"}},
	} {
		for _, preference := range []bool{false, true} {
			in := fixture(t)
			restrict(&in, "alpha", "super")
			in.Host, in.Story = "current", "current"
			in.Task = TaskProfile{Role: "reviewer", TaskClass: "review.code", Difficulty: "hard", Sensitivity: "normal"}
			for i := range in.Catalog.Rows {
				r := &in.Catalog.Rows[i]
				if r.Model == "alpha" {
					r.Quality.Review = reviewValue(35, 1, 3)
				}
				if r.Model == "super" {
					r.Quality.Review = reviewValue(55, 1, 3)
				}
			}
			in.Policy.Rules = []Rule{{ID: "legacy", Source: "operator", Prefer: &RulePreference{Runtimes: []string{"rt-a", "rt-s"}}}}
			before := run(t, in)
			selected(t, before, "super")
			unrelated := Rule{ID: "unrelated", Source: "operator", When: when, Forbid: &RuleSelector{Runtime: "absent"}}
			if preference {
				unrelated.Forbid = nil
				unrelated.Prefer = &RulePreference{Runtimes: []string{"rt-a", "rt-s"}}
			}
			in.Policy.Rules = append(in.Policy.Rules, unrelated)
			after := run(t, in)
			selected(t, after, "super")
			if !reflect.DeepEqual(before.Alternatives, after.Alternatives) || !reflect.DeepEqual(before.Explanation, after.Explanation) || !reflect.DeepEqual(before.AppliedRules, after.AppliedRules) || len(after.SkippedRules) != 1 {
				t.Fatalf("nonmatching rule changed candidate ranking: %+v", when)
			}
		}
	}
}

func TestScopedReviewPreferenceKeepsQualityFirst(t *testing.T) {
	for _, mode := range []string{"balanced", "burn"} {
		for _, task := range []TaskProfile{
			{Role: "reviewer", TaskClass: "review.code", Difficulty: "hard"},
			{Role: "reviewer", TaskClass: "review.code", Difficulty: "critical"},
			{Role: "reviewer", TaskClass: "review.code", Difficulty: "standard", Sensitivity: "delicate"},
		} {
			for _, gap := range []float64{0, 0.5, 1, 20} {
				in := fixture(t)
				restrict(&in, "alpha", "super")
				in.Host, in.Task, in.Policy.BudgetMode = "current", task, mode
				in.Policy.Rules = []Rule{{ID: "preferred", Source: "operator", When: RuleWhen{Host: "current", Difficulty: []string{"standard", "hard", "critical"}}, Prefer: &RulePreference{Runtimes: []string{"rt-a", "rt-s"}}}}
				for i := range in.Catalog.Rows {
					r := &in.Catalog.Rows[i]
					if r.Model == "alpha" {
						r.Quality.Review = reviewValue(55-gap, 1, 3)
						r.Cost.USDPerTask = ptr(10.0)
					}
					if r.Model == "super" {
						r.Quality.Review = reviewValue(55, 1, 3)
						r.Cost.USDPerTask = ptr(1.0)
					}
				}
				want := "super"
				if gap < 1 {
					want = "alpha" // Prefer only within the anchor's best-quality band.
				}
				d, err := BuildDecision(in)
				if err != nil {
					t.Fatal(err)
				}
				selected(t, d.Recommendation, want)
				if err := Replay(d); err != nil {
					t.Fatal(err)
				}
				slices.Reverse(in.Candidates)
				slices.Reverse(in.Catalog.Rows)
				again, err := BuildDecision(in)
				if err != nil || !reflect.DeepEqual(d, again) {
					t.Fatal("nondeterministic preference", err)
				}
			}
		}
	}
}
