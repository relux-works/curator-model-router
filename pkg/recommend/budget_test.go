package recommend

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/routing"
)

func buildHostRules() []Rule {
	return []Rule{
		{ID: "host-codex-first", Source: "operator ruling: host reviewer pin (example)", When: RuleWhen{Host: "build-host", Difficulty: []string{"trivial", "routine", "standard", "hard", "critical"}}, Prefer: &RulePreference{Runtimes: []string{"codex", "claude", "agy", "muse", "local-qwen"}}},
		{ID: "host-review-astra-medium", Source: "operator ruling: host reviewer pin (example)", When: RuleWhen{Host: "build-host", Role: "reviewer", Difficulty: []string{"trivial", "routine", "standard"}, Sensitivity: []string{"normal"}}, Require: &RuleSelector{Runtime: "codex", Model: "gpt-6-astra"}, Effort: &RuleEffort{Pin: "medium"}},
	}
}

func TestBudgetPickTable(t *testing.T) {
	var expected map[string]struct {
		Candidate string `json:"candidate"`
		Tier      Tier   `json:"tier"`
	}
	raw, err := os.ReadFile("testdata/budget-picks.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 60 {
		t.Fatal("incomplete mode/difficulty pick table")
	}
	for _, role := range []string{"developer", "reviewer"} {
		for _, buildHost := range []bool{false, true} {
			for _, mode := range []string{"economy", "balanced", "burn"} {
				for _, difficulty := range []string{"trivial", "routine", "standard", "hard", "critical"} {
					t.Run(role+"/"+mode+"/"+difficulty+map[bool]string{true: "/build-host", false: ""}[buildHost], func(t *testing.T) {
						in := realRequest(t)
						in.Task.Role = role
						if role == "reviewer" {
							in.Task.TaskClass = "review.code"
						}
						in.Task.Difficulty = difficulty
						in.Policy.BudgetMode = mode
						if buildHost {
							in.Host = "build-host"
							in.Policy.Rules = buildHostRules()
						}
						d, err := BuildDecision(in)
						if err != nil {
							t.Fatal(err)
						}
						x := chosenExplanation(t, d.Recommendation)
						key := role + "/" + map[bool]string{true: "build-host", false: "default"}[buildHost] + "/" + mode + "/" + difficulty
						want := expected[key]
						pick := x.Candidate.Runtime + "/" + x.Candidate.Model + "/" + x.Candidate.Effort
						t.Logf("PICK | %s | %t | %s | %s | %s/%s/%s | %s", role, buildHost, mode, difficulty, x.Candidate.Runtime, x.Candidate.Model, x.Candidate.Effort, x.Tier)
						if os.Getenv("PUBED_UPDATE") == "1" {
							expected[key] = struct {
								Candidate string `json:"candidate"`
								Tier      Tier   `json:"tier"`
							}{pick, x.Tier}
						} else if pick != want.Candidate || x.Tier != want.Tier {
							t.Fatalf("%s: want %+v, got %s (%s)", key, want, pick, x.Tier)
						}
						floor := in.Policy.Difficulties[difficulty].MinimumTier
						if tierRank(x.Tier) < tierRank(floor) {
							t.Fatal("tier floor violated", x)
						}
						if buildHost && role == "reviewer" {
							if difficulty == "hard" || difficulty == "critical" {
								if !strings.Contains(d.Recommendation.RenderHuman(), "rule_not_applicable: difficulty") {
									t.Fatal("missing skipped rule explanation")
								}
							} else if x.Candidate != (Candidate{"codex", "gpt-6-astra", "medium"}) {
								t.Fatal("host reviewer pin lost", x)
							}
						}
						if err = Replay(d); err != nil {
							t.Fatal(err)
						}
						slices.Reverse(in.Candidates)
						slices.Reverse(in.Catalog.Rows)
						again, err := BuildDecision(in)
						if err != nil || !reflect.DeepEqual(d, again) {
							t.Fatal("nondeterministic decision", err)
						}
					})
				}
			}
		}
	}
	if os.Getenv("PUBED_UPDATE") == "1" {
		raw, err := json.MarshalIndent(expected, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile("testdata/budget-picks.json", append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScopedReviewFallbacks(t *testing.T) {
	for _, mode := range []string{"economy", "balanced", "burn"} {
		for _, delicate := range []bool{false, true} {
			in := realRequest(t)
			in.Host = "build-host"
			in.Policy.Rules = buildHostRules()
			in.Policy.BudgetMode = mode
			in.Task = TaskProfile{Role: "reviewer", TaskClass: "review.code", Difficulty: "hard"}
			if delicate {
				in.Task.Difficulty = "standard"
				in.Task.Sensitivity = "delicate"
			}
			got := run(t, in)
			x := chosenExplanation(t, got)
			if tierRank(x.Tier) < tierRank(TierA) {
				t.Fatal(got.RenderHuman())
			}
			if delicate && !strings.Contains(got.RenderHuman(), "rule_not_applicable: sensitivity") {
				t.Fatal(got.RenderHuman())
			}
			// Codex unavailable: a qualified other provider must still be selected.
			in.Candidates = slices.DeleteFunc(in.Candidates, func(c Candidate) bool { return c.Runtime == "codex" })
			x = chosenExplanation(t, run(t, in))
			if x.Candidate.Runtime != "claude" || tierRank(x.Tier) < tierRank(TierA) {
				t.Fatal(x)
			}
			// Only one A review remains: neither the pin nor delicate's S preference may refuse it.
			in = realRequest(t)
			in.Host = "build-host"
			in.Policy.Rules = buildHostRules()
			in.Policy.BudgetMode = mode
			in.Task = TaskProfile{Role: "reviewer", TaskClass: "review.code", Difficulty: "hard"}
			if delicate {
				in.Task.Sensitivity = "delicate"
			}
			in.Candidates = []Candidate{{"codex", "gpt-6.1-sol", "high"}}
			chosenExplanation(t, run(t, in))
		}
	}
}

func TestBudgetObjectivesAndIdentity(t *testing.T) {
	in := fixture(t)
	restrict(&in, "alpha", "delta")
	in.Task.Difficulty = "standard"
	// Delta is A and within 1.5x alpha, which is B.
	for i := range in.Catalog.Rows {
		r := &in.Catalog.Rows[i]
		if r.Model == "alpha" {
			r.Quality.Coding.Value = 50
			r.Cost.USDPerTask = ptr(1.0)
		}
		if r.Model == "delta" {
			r.Quality.Coding.Value = 65
			r.Cost.USDPerTask = ptr(1.4)
		}
	}
	selected(t, run(t, in), "delta")
	balanced, err := BuildDecision(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Policy.BudgetMode = "economy"
	selected(t, run(t, in), "alpha")
	economy, err := BuildDecision(in)
	if err != nil {
		t.Fatal(err)
	}
	if balanced.DecisionID == economy.DecisionID || economy.Recommendation.BudgetMode != "economy" {
		t.Fatal("mode not bound to decision")
	}
	for _, difficulty := range []string{"trivial", "routine", "standard", "hard", "critical"} {
		in := fixture(t)
		restrict(&in, "alpha", "super")
		in.Task.Difficulty = difficulty
		in.Policy.BudgetMode = "burn"
		selected(t, run(t, in), "super")
	}
}

func TestBurnHeadroomTie(t *testing.T) {
	for _, review := range []bool{false, true} {
		for _, freshness := range []routing.Freshness{routing.Fresh, routing.Stale, routing.Unknown} {
			in := fixture(t)
			restrict(&in, "alpha", "super")
			in.Task.Difficulty = "hard"
			if review {
				in.Task.TaskClass = "review.code"
			}
			for i := range in.Catalog.Rows {
				r := &in.Catalog.Rows[i]
				if r.Model == "alpha" || r.Model == "super" {
					r.Quality.Coding.Value = 80
					r.Cost.USDPerTask = ptr(1.0)
					if review {
						r.Quality.Review = reviewValue(50, 2, 3)
					}
					if r.Model == "super" {
						r.Cost.USDPerTask = ptr(10.0)
					}
				}
			}
			quota(&in, "alpha", 4000, 300, 0, routing.Fresh)
			if freshness != routing.Unknown {
				quota(&in, "super", 1000, 9000, 0, freshness)
			}
			selected(t, run(t, in), "alpha")
			in.Policy.BudgetMode = "burn"
			want := "alpha"
			if freshness == routing.Fresh {
				want = "super"
			}
			selected(t, run(t, in), want)
		}
	}
	// A quality advantage must beat headroom, including a review gap at the SE boundary.
	in := fixture(t)
	restrict(&in, "alpha", "super")
	in.Task.TaskClass = "review.code"
	in.Task.Difficulty = "hard"
	in.Policy.BudgetMode = "burn"
	for i := range in.Catalog.Rows {
		r := &in.Catalog.Rows[i]
		if r.Model == "alpha" {
			r.Quality.Review = reviewValue(50, 2, 3)
		}
		if r.Model == "super" {
			r.Quality.Review = reviewValue(52, 2, 3)
		}
	}
	quota(&in, "alpha", 0, 300, 0, routing.Fresh)
	quota(&in, "super", 5000, 9000, 0, routing.Fresh)
	selected(t, run(t, in), "super")
}

func TestBurnFanoutCap(t *testing.T) {
	in := fixture(t)
	restrict(&in, "alpha", "delta", "super", "sigma")
	in.Task.Pipeline = "fanout"
	for i := range in.Catalog.Rows {
		r := &in.Catalog.Rows[i]
		if r.Quality.Coding == nil {
			continue
		}
		r.Quality.Coding.Value = 80
		r.Quality.Coding.Stderr = nil
		r.Constraints = Constraints{}
		r.Billing = routing.BillingSubscription
		r.Family = r.Model
	}
	assertFanout(t, run(t, in), 3)
	in.Policy.BudgetMode = "burn"
	assertFanout(t, run(t, in), 4)
	in.Policy.BurnFanOutCap = 2
	assertFanout(t, run(t, in), 2)
}

func TestBudgetAndScopeValidation(t *testing.T) {
	for _, raw := range []string{
		`{"budget_mode":"auto"}`, `{"budget_mode":"cheap"}`, `{"burn_fanout_cap":-1}`, `{"burn_fanout_cap":101}`,
		`{"rules":[{"id":"r","source":"s","exclude":true,"when":{"difficulty":["easy"]}}]}`,
		`{"rules":[{"id":"r","source":"s","exclude":true,"when":{"sensitivity":["sensitive"]}}]}`,
		`{"rules":[{"id":"r","source":"s","exclude":true,"when":{"difficulty":[]}}]}`,
		`{"rules":[{"id":"r","source":"s","exclude":true,"when":{"difficulty":["hard","hard"]}}]}`,
	} {
		_, err := LoadPolicy([]byte(raw))
		expectCode(t, err, InvalidPolicy)
	}
	p, err := LoadPolicy([]byte(`{"budget_mode":"burn","burn_fanout_cap":5}`))
	if err != nil || p.EffectiveBudgetMode() != "burn" || p.EffectiveBurnFanOutCap() != 5 {
		t.Fatal(p, err)
	}
}

func TestScopedReviewPreferenceAnchoredQuality(t *testing.T) {
	in := fixture(t)
	restrict(&in, "alpha", "delta", "super")
	in.Task = TaskProfile{Role: "reviewer", TaskClass: "review.code", Difficulty: "hard"}
	for i := range in.Catalog.Rows {
		row := &in.Catalog.Rows[i]
		switch row.Model {
		case "alpha":
			row.Quality.Review = reviewValue(50, 5, 3)
			row.Cost.USDPerTask = ptr(1.0)
		case "delta":
			row.Runtime = "rt-a"
			row.Quality.Review = reviewValue(45, 5, 3)
			row.Cost.USDPerTask = ptr(0.2)
		case "super":
			row.Quality.Review = reviewValue(54, 10, 3)
			row.Cost.USDPerTask = ptr(2.0)
		}
	}
	in.Candidates = []Candidate{{"rt-a", "alpha", "high"}, {"rt-a", "delta", "high"}, {"rt-s", "super", "max"}}
	in.Policy.Rules = []Rule{{ID: "first", Source: "operator", When: RuleWhen{Difficulty: []string{"hard"}}, Prefer: &RulePreference{Runtimes: []string{"rt-a", "rt-s"}}}}
	// Anchor on the highest mean across providers. All three tie with super,
	// so prefer rt-a and then its cheaper delta; never re-anchor by runtime.
	selected(t, run(t, in), "delta")
	// No runtime preference may outrank a stronger quality band.
	in.Policy.Rules[0].Prefer.Runtimes = []string{"codex", "rt-a", "rt-s"}
	for i := range in.Catalog.Rows {
		if in.Catalog.Rows[i].Model == "super" {
			in.Catalog.Rows[i].Quality.Review = reviewValue(70, 1, 3)
		}
	}
	selected(t, run(t, in), "super")
}

func TestEconomyKeepsCostAheadOfHeadroom(t *testing.T) {
	in := fixture(t)
	restrict(&in, "alpha", "delta")
	in.Task.Difficulty = "hard"
	in.Policy.BudgetMode = "economy"
	quota(&in, "alpha", 7000, 9000, 0, routing.Fresh)
	quota(&in, "delta", 1000, 300, 0, routing.Fresh)
	selected(t, run(t, in), "alpha")
}
