package recommend

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/routing"
)

func realRequest(t testing.TB) Request {
	t.Helper()
	c, err := LoadCatalog(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Rows) != 46 {
		t.Fatalf("catalog rows=%d", len(c.Rows))
	}
	r := Request{Catalog: c, Policy: DefaultPolicy(), Task: TaskProfile{Role: "developer", TaskClass: "code.implement"}, Usage: UsageSnapshot{AsOf: 1800000000}}
	for _, row := range c.Rows {
		r.Candidates = append(r.Candidates, row.Candidate)
	}
	return r
}
func chosenExplanation(t testing.TB, r Recommendation) CandidateExplanation {
	t.Helper()
	if r.Selected == nil || r.Refusal != nil {
		t.Fatalf("missing selection: %s", r.RenderHuman())
	}
	for _, x := range r.Explanation {
		if x.Candidate == *r.Selected {
			return x
		}
	}
	t.Fatal("missing selected explanation")
	return CandidateExplanation{}
}
func TestEmbeddedCatalogAcceptance(t *testing.T) {
	t.Run("trivial_cheap_low_effort", func(t *testing.T) {
		r := realRequest(t)
		r.Task.Difficulty = "trivial"
		got := run(t, r)
		x := chosenExplanation(t, got)
		if x.Candidate != (Candidate{"codex", "gpt-6-luna", "medium"}) || x.Cost.USDPerTask == nil || *x.Cost.USDPerTask >= .10 {
			t.Fatal(x)
		}
	})
	t.Run("hard_a_or_above", func(t *testing.T) {
		r := realRequest(t)
		r.Task.Difficulty = "hard"
		if tierRank(chosenExplanation(t, run(t, r)).Tier) < tierRank(TierA) {
			t.Fatal("below A")
		}
	})
	t.Run("delicate_strongest_s", func(t *testing.T) {
		r := realRequest(t)
		r.Task.Sensitivity = "delicate"
		got := run(t, r)
		x := chosenExplanation(t, got)
		if x.Tier != TierS || x.Candidate != (Candidate{"claude", "claude-sonnet-5-5", "max"}) {
			t.Fatal(x)
		}
	})
	t.Run("muse_quality_absent_refuses", func(t *testing.T) {
		r := realRequest(t)
		restrict(&r, "muse-spark-1.3-contributor")
		got := run(t, r)
		if got.Refusal == nil || got.Selected != nil {
			t.Fatal(got)
		}
		for _, row := range r.Catalog.Rows {
			if row.Runtime == "muse" && (row.Effort != "max" || row.Constraints.OnlyEffort != "max") {
				t.Fatal(row)
			}
		}
	})
	t.Run("exhausted_moves_to_equivalent", func(t *testing.T) {
		r := realRequest(t)
		// These measured A rows have costs within the default 25% band, so usage can rotate.
		r.Candidates = []Candidate{{"claude", "claude-sonnet-5-5", "high"}, {"codex", "gpt-6-astra", "high"}}
		r.Usage.UsageKeys = map[string]string{}
		for i, c := range r.Candidates {
			key := c.Key()
			r.Usage.UsageKeys[key] = key
			used := int64(10000)
			if i == 1 {
				used = 2000
			}
			r.Usage.Facts = append(r.Usage.Facts, routing.UsageFact{Key: key, Runtime: c.Runtime, State: routing.StateExact, RecordDigest: ptr("sha256:" + strings.Repeat("a", 64)), Windows: []routing.UsageWindow{{ID: "session", Scope: "all", Minutes: 300, UsedBP: ptr(used), ObservedAt: ptr(r.Usage.AsOf), ResetsAt: ptr(r.Usage.AsOf + 9000), Freshness: routing.Fresh}}})
		}
		got := run(t, r)
		if chosenExplanation(t, got).Candidate != r.Candidates[1] {
			t.Fatal(got.RenderHuman())
		}
	})
	t.Run("fanout_default_catalog_has_two_a_families", func(t *testing.T) {
		r := realRequest(t)
		r.Task.Pipeline = "fanout"
		got := run(t, r)
		if len(got.FanOut) != 2 {
			t.Fatal(got.RenderHuman())
		}
		assertFanout(t, got, 2)
	})
	t.Run("fanout_does_not_invent_muse_quality", func(t *testing.T) {
		r := realRequest(t)
		r.Task.Pipeline = "fanout"
		r.Policy.ReviewTierEdges.A = 10
		r.Policy.ReviewTierEdges.B = 5
		got := run(t, r)
		assertFanout(t, got, 3)
		for _, c := range got.FanOut {
			if c.Runtime == "muse" {
				t.Fatal("unmeasured family admitted")
			}
		}
	})
	t.Run("metered_off", func(t *testing.T) {
		r := realRequest(t)
		if chosenExplanation(t, run(t, r)).Billing == routing.BillingMetered {
			t.Fatal("metered chosen from default catalog")
		}
		// The real catalog is entirely subscription/local. Project its admitted
		// rows as metered to exercise the off guard without inventing a model.
		for i := range r.Catalog.Rows {
			r.Catalog.Rows[i].Billing = routing.BillingMetered
		}
		got := run(t, r)
		if got.Refusal == nil || got.Selected != nil {
			t.Fatal("metered selected when off")
		}
		for _, x := range got.Explanation {
			if !slices.Contains(x.ReasonCodes, "metered_disabled") {
				t.Fatal(x)
			}
		}
	})
	t.Run("locks", func(t *testing.T) {
		r := realRequest(t)
		r.Locks = Locks{Agent: "codex", Model: "gpt-6.1-sol", Effort: "high"}
		if chosenExplanation(t, run(t, r)).Candidate != (Candidate{"codex", "gpt-6.1-sol", "high"}) {
			t.Fatal("locks ignored")
		}
	})
	t.Run("empty_refuses", func(t *testing.T) {
		r := realRequest(t)
		r.Candidates = nil
		got := run(t, r)
		if got.Refusal == nil || got.Refusal.Code != NoQualifiedCandidate || got.Selected != nil {
			t.Fatal(got)
		}
	})
	t.Run("calibration_and_stderr", func(t *testing.T) {
		r := realRequest(t)
		if r.Policy.TierEdges != (TierEdges{62, 56, 45}) {
			t.Fatal(r.Policy.TierEdges)
		}
		for _, row := range r.Catalog.Rows {
			if row.Runtime == "muse" && TierFor(taskQuality(row, "code.implement"), r.Policy) != TierU {
				t.Fatal("stderr ignored")
			}
		}
		p, err := LoadPolicy([]byte(`{"tier_edges":{"s":75,"a":60}}`))
		if err != nil || p.TierEdges != (TierEdges{75, 60, 45}) {
			t.Fatal(p, err)
		}
	})
}
func assertFanout(t testing.TB, got Recommendation, n int) {
	t.Helper()
	families := map[string]bool{}
	if len(got.FanOut) != n {
		t.Fatalf("fanout=%d want %d: %s", len(got.FanOut), n, got.RenderHuman())
	}
	for _, c := range got.FanOut {
		for _, x := range got.Explanation {
			if x.Candidate == c {
				if families[x.Family] || tierRank(x.Tier) < tierRank(TierA) {
					t.Fatal(x)
				}
				families[x.Family] = true
			}
		}
	}
}
func rule(action func(*Rule)) Rule {
	r := Rule{ID: "ruling", Source: "tb-test", Rationale: "Test operator ruling."}
	action(&r)
	return r
}
func TestStandingRuleActions(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule Rule
		want string
	}{
		{"require_runtime", rule(func(r *Rule) { r.Require = &RuleSelector{Runtime: "rt-d"} }), "delta"},
		{"require_model", rule(func(r *Rule) { r.Require = &RuleSelector{Model: "delta"} }), "delta"},
		{"require_family", rule(func(r *Rule) { r.Require = &RuleSelector{Family: "four"} }), "delta"},
		{"require_effort", rule(func(r *Rule) { r.Require = &RuleSelector{Effort: "max"} }), "super"},
		{"forbid_runtime", rule(func(r *Rule) { r.Forbid = &RuleSelector{Runtime: "rt-a"} }), "delta"},
		{"forbid_model", rule(func(r *Rule) { r.Forbid = &RuleSelector{Model: "alpha"} }), "delta"},
		{"forbid_family", rule(func(r *Rule) { r.Forbid = &RuleSelector{Family: "one"} }), "delta"},
		{"forbid_effort", rule(func(r *Rule) { r.Forbid = &RuleSelector{Effort: "high"} }), "super"},
		{"effort_pin", rule(func(r *Rule) { r.Effort = &RuleEffort{Pin: "max"} }), "super"},
		{"effort_range", rule(func(r *Rule) { r.Effort = &RuleEffort{Min: "high", Max: "xhigh"} }), "alpha"},
		{"prefer_runtime", rule(func(r *Rule) { r.Prefer = &RulePreference{Runtimes: []string{"rt-d", "rt-a"}} }), "delta"},
		{"prefer_family", rule(func(r *Rule) { r.Prefer = &RulePreference{Families: []string{"four", "one"}} }), "delta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t)
			restrict(&r, "alpha", "delta", "super")
			r.Policy.Rules = []Rule{tc.rule}
			got := run(t, r)
			selected(t, got, tc.want)
			if len(got.AppliedRules) != 1 || got.AppliedRules[0].Source != "tb-test" || !strings.Contains(got.RenderHuman(), "source=tb-test") {
				t.Fatal("missing source", got)
			}
		})
	}
}
func TestRuleMatchingAndHostFallback(t *testing.T) {
	r := fixture(t)
	restrict(&r, "alpha", "delta")
	r.Host = "build-host"
	r.Story = "story-123"
	ruling := rule(func(rule *Rule) {
		rule.When = RuleWhen{Host: "build-host", Role: "developer", TaskClass: "code.implement", Runtime: "rt-a", Model: "alpha", Story: "story-123"}
		rule.Forbid = &RuleSelector{Model: "alpha"}
	})
	r.Policy.Rules = []Rule{ruling}
	selected(t, run(t, r), "delta")
	for _, field := range []string{"host", "role", "class", "runtime", "model", "story"} {
		t.Run(field, func(t *testing.T) {
			local := r
			other := ruling
			switch field {
			case "host":
				other.When.Host = "other"
			case "role":
				other.When.Role = "other"
			case "class":
				other.When.TaskClass = "other"
			case "runtime":
				other.When.Runtime = "other"
			case "model":
				other.When.Model = "other"
			case "story":
				other.When.Story = "other"
			}
			local.Policy.Rules = []Rule{other}
			got := run(t, local)
			selected(t, got, "alpha")
			if len(got.AppliedRules) != 0 {
				t.Fatal(got.AppliedRules)
			}
		})
	}
	r.Host = ""
	r.Policy.Host = "build-host"
	selected(t, run(t, r), "delta")
	r.Host = "second-host"
	selected(t, run(t, r), "alpha")
}
func TestRulePrecedenceRefusalAndReplay(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		r := fixture(t)
		restrict(&r, "alpha", "delta")
		require := Rule{ID: "require", Source: "tb-require", Require: &RuleSelector{Model: "alpha"}}
		forbid := Rule{ID: "forbid", Source: "tb-forbid", Forbid: &RuleSelector{Model: "alpha"}}
		r.Policy.Rules = []Rule{require, forbid}
		if reverse {
			slices.Reverse(r.Policy.Rules)
		}
		d, err := BuildDecision(r)
		if err != nil {
			t.Fatal(err)
		}
		got := d.Recommendation
		last := r.Policy.Rules[1]
		if got.Refusal == nil || got.Selected != nil || !strings.Contains(got.Refusal.Message, last.ID) || !strings.Contains(got.Refusal.Message, last.Source) || len(got.AppliedRules) != 2 {
			t.Fatal(got)
		}
		raw, err := d.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = LoadDecision(raw); err != nil {
			t.Fatal(err)
		}
	}
	r := fixture(t)
	restrict(&r, "alpha", "delta")
	r.Policy.Rules = []Rule{{ID: "both", Source: "tb-both", Require: &RuleSelector{Model: "alpha"}, Forbid: &RuleSelector{Model: "alpha"}}}
	if got := run(t, r); got.Refusal == nil || !strings.Contains(got.Refusal.Message, "both") {
		t.Fatal(got)
	}
	// A soft preference cannot move an S row into an A slot, and earlier rules win.
	r = fixture(t)
	restrict(&r, "alpha", "delta", "super")
	r.Task.Difficulty = "hard"
	r.Policy.Rules = []Rule{{ID: "first", Source: "tb-first", Prefer: &RulePreference{Families: []string{"four"}}}, {ID: "second", Source: "tb-second", Prefer: &RulePreference{Families: []string{"one"}}}}
	got := run(t, r)
	selected(t, got, "super")
	if got.Alternatives[0].Candidate.Model != "delta" {
		t.Fatal(got.Alternatives)
	}
	// Locks and floors remain hard constraints even when an operator requires a row.
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	r.Locks.Model = "alpha"
	r.Policy.Rules = []Rule{{ID: "pin", Source: "tb-pin", Require: &RuleSelector{Model: "delta"}}}
	if got := run(t, r); got.Refusal == nil || !strings.Contains(got.Refusal.Message, "pin") {
		t.Fatal(got)
	}
	r = fixture(t)
	restrict(&r, "mini", "alpha")
	r.Policy.Rules = []Rule{{ID: "below", Source: "tb-below", Require: &RuleSelector{Model: "mini"}}}
	if got := run(t, r); got.Refusal == nil || !strings.Contains(got.Refusal.Message, "below") {
		t.Fatal(got)
	}
}
func TestQuotaStopKnownWindows(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(*Request)
		stopped bool
	}{
		{"inclusive_stop", func(r *Request) { quota(r, "alpha", 7500, 9000, 0, routing.Fresh) }, true},
		{"above_stop", func(r *Request) { quota(r, "alpha", 7499, 9000, 0, routing.Fresh) }, false},
		{"exhausted", func(r *Request) { quota(r, "alpha", 11000, 9000, 0, routing.Fresh) }, true},
		{"unknown", func(r *Request) {}, false},
		{"stale", func(r *Request) { quota(r, "alpha", 10000, 9000, 0, routing.Stale) }, false},
		{"expired", func(r *Request) { quota(r, "alpha", 10000, 0, 0, routing.Expired) }, false},
		{"missing_used", func(r *Request) {
			quota(r, "alpha", 10000, 9000, 0, routing.Fresh)
			r.Usage.Facts[0].Windows[0].UsedBP = nil
			r.Usage.Facts[0].Windows[0].Freshness = routing.Invalid
		}, false},
		{"unmapped", func(r *Request) {
			quota(r, "alpha", 10000, 9000, 0, routing.Fresh)
			r.Usage.Facts[0].Windows[0].Scope = "other"
		}, false},
		{"scope_excludes_model", func(r *Request) {
			quota(r, "alpha", 10000, 9000, 0, routing.Fresh)
			r.Usage.Facts[0].Windows[0].Scope = "other"
			r.Usage.ScopeMap.Entries = []routing.ScopeMapEntry{{Runtime: "rt-a", Scope: "other", ModelIDs: []string{"delta"}}}
		}, false},
		{"scope_includes_model", func(r *Request) {
			quota(r, "alpha", 10000, 9000, 0, routing.Fresh)
			r.Usage.Facts[0].Windows[0].Scope = "other"
			r.Usage.ScopeMap.Entries = []routing.ScopeMapEntry{{Runtime: "rt-a", Scope: "other", ModelIDs: []string{"alpha"}}}
		}, true},
		{"window_filtered", func(r *Request) {
			quota(r, "alpha", 10000, 9000, 0, routing.Fresh)
			r.Policy.Headroom.Windows = routing.WindowFilter{IDs: []string{"weekly"}}
		}, false},
		{"tightest_weekly_with_unknown_companion", func(r *Request) {
			quota(r, "alpha", 2000, 9000, 0, routing.Fresh)
			w := r.Usage.Facts[0].Windows[0]
			w.ID = "weekly"
			w.UsedBP = ptr(int64(8000))
			r.Usage.Facts[0].Windows = append(r.Usage.Facts[0].Windows, w)
			w.ID = "unknown"
			w.UsedBP = nil
			w.Freshness = routing.Invalid
			r.Usage.Facts[0].Windows = append(r.Usage.Facts[0].Windows, w)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t)
			restrict(&r, "alpha", "delta")
			r.Policy.Headroom.Enabled = false
			r.Policy.Rules = []Rule{{ID: "quota", Source: "operator ruling: quota stop (example)", When: RuleWhen{Runtime: "rt-a"}, QuotaStopBP: ptr(int64(2500))}}
			tc.edit(&r)
			want := "alpha"
			if tc.stopped {
				want = "delta"
			}
			got := run(t, r)
			selected(t, got, want)
			if slices.Contains(explanation(t, got, "alpha").ReasonCodes, "rule:quota:quota_stop") != tc.stopped {
				t.Fatal(got)
			}
		})
	}
	r := fixture(t)
	restrict(&r, "alpha")
	quota(&r, "alpha", 7500, 9000, 0, routing.Fresh)
	r.Policy.Rules = []Rule{{ID: "stop", Source: "operator ruling: quota stop (example)", QuotaStopBP: ptr(int64(2500))}}
	if got := run(t, r); got.Refusal == nil || !strings.Contains(got.Refusal.Message, "operator ruling: quota stop (example)") {
		t.Fatal(got)
	}
	// Local and metered candidates never consume subscription quota facts.
	for _, billing := range []routing.BillingClass{routing.BillingLocal, routing.BillingMetered} {
		r = fixture(t)
		restrict(&r, "alpha")
		r.Policy.AllowMetered = true
		quota(&r, "alpha", 10000, 9000, 0, routing.Fresh)
		for i := range r.Catalog.Rows {
			if r.Catalog.Rows[i].Model == "alpha" {
				r.Catalog.Rows[i].Billing = billing
			}
		}
		r.Policy.Rules = []Rule{{ID: "stop", Source: "operator ruling: quota stop (example)", QuotaStopBP: ptr(int64(2500))}}
		selected(t, run(t, r), "alpha")
	}
}
func TestCrossProviderReview(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		role, producer, host string
		models               []string
		fallback             bool
		want                 string
		refused              bool
	}{
		{name: "other_family", role: "reviewer", producer: "one", models: []string{"alpha", "delta"}, want: "delta"},
		{name: "explicit_last_resort", role: "reviewer", producer: "one", models: []string{"alpha"}, fallback: true, want: "alpha"},
		{name: "no_implicit_fallback", role: "reviewer", producer: "one", models: []string{"alpha"}, refused: true},
		{name: "missing_producer", role: "reviewer", models: []string{"alpha", "delta"}, want: "alpha"},
		{name: "not_reviewer", role: "developer", producer: "one", models: []string{"alpha", "delta"}, want: "alpha"},
		{name: "host_exception", role: "reviewer", producer: "one", host: "build-host", models: []string{"alpha", "delta"}, want: "alpha"},
		{name: "unqualified_other_cannot_suppress_fallback", role: "reviewer", producer: "one", models: []string{"alpha", "mini"}, fallback: true, want: "alpha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t)
			restrict(&r, tc.models...)
			r.Task.Role = tc.role
			r.ProducerFamily = tc.producer
			r.Host = tc.host
			r.Policy.Rules = []Rule{{ID: "review", Source: "operator ruling: cross-provider review (example)", CrossProviderReview: &CrossProviderReview{AllowSameProviderFallback: tc.fallback, ExceptHosts: []string{"build-host"}}}}
			got := run(t, r)
			if tc.refused {
				if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "review") {
					t.Fatal(got)
				}
			} else {
				selected(t, got, tc.want)
			}
		})
	}
}
func TestRotationExclusion(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		r := fixture(t)
		restrict(&r, "alpha", "delta")
		r.Exclude = []string{"rt-a/alpha"}
		source := "--exclude"
		if explicit {
			r.Policy.Rules = []Rule{{ID: "rotate", Source: "operator ruling: refusal rotation (example)", Exclude: true}}
			source = "operator ruling: refusal rotation (example)"
		}
		got := run(t, r)
		selected(t, got, "delta")
		if len(got.AppliedRules) != 1 || got.AppliedRules[0].Source != source {
			t.Fatal(got.AppliedRules)
		}
		r.Exclude = append(r.Exclude, "rt-d/delta")
		if got = run(t, r); got.Refusal == nil || !strings.Contains(got.Refusal.Message, source) {
			t.Fatal(got)
		}
	}
	r := fixture(t)
	r.Exclude = []string{"bad"}
	_, err := Recommend(r)
	expectCode(t, err, InvalidInput)
}
func TestRuleJSONValidationAndIsolation(t *testing.T) {
	raw := []byte(`{"host":"build-host","rules":[{"id":"json-rule","source":"tb-json","rationale":"Exact policy decode.","when":{"host":"build-host","role":"developer","task_class":"code.implement","story":"story-123"},"require":{"family":"openai"},"forbid":{"model":"gpt-6-luna"},"effort":{"min":"low","max":"high"},"prefer":{"runtimes":["codex"],"families":["openai"]},"quota_stop_bp":2500,"cross_provider_review":{"allow_same_provider_fallback":true,"except_hosts":["build-host"]},"exclude":true}]}`)
	p, err := LoadPolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := realRequest(t)
	r.Policy = p
	r.Story = "story-123"
	r.ProducerFamily = "openai"
	r.Exclude = []string{"codex/gpt-6-sol"}
	d, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Inputs.Host != "build-host" || len(d.Recommendation.AppliedRules) != 1 {
		t.Fatalf("host=%q applied rules=%v", d.Inputs.Host, d.Recommendation.AppliedRules)
	}
	p.Rules[0].Prefer.Runtimes[0] = "mutated"
	p.Rules[0].Require.Family = "mutated"
	r.Exclude[0] = "mutated"
	if err = Replay(d); err != nil {
		t.Fatal("rule input alias", err)
	}
	encoded, err := d.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = LoadDecision(encoded); err != nil {
		t.Fatal(err)
	}
	d.Recommendation.AppliedRules[0].Source = "substituted"
	id, err := d.ContentID()
	if err != nil {
		t.Fatal(err)
	}
	d.DecisionID = id
	d.Recommendation.DecisionID = id
	expectCode(t, Replay(d), DecisionMismatch)
	for _, raw := range []string{
		`{"rules":[{"id":"r","source":"s","when":{}}]}`,
		`{"rules":[{"id":"r","source":"s","when":{},"require":{}}]}`,
		`{"rules":[{"id":"r","source":"s","when":{},"effort":{"pin":"high","min":"low"}}]}`,
		`{"rules":[{"id":"r","source":"s","when":{},"effort":{"min":"max","max":"low"}}]}`,
		`{"rules":[{"id":"r","source":"s","when":{},"quota_stop_bp":10001}]}`,
		`{"rules":[{"id":"r","source":"s","when":{},"prefer":{"runtimes":["codex","codex"]}}]}`,
		`{"rules":[{"id":"r","source":"s","when":{"Host":"build-host"},"exclude":true}]}`,
		`{"rules":[{"id":"r","source":"s","when":{},"exclude":true},{"id":"r","source":"s","when":{},"exclude":true}]}`,
		`{"rules":[{"id":"r","when":{},"exclude":true}]}`,
	} {
		_, err := LoadPolicy([]byte(raw))
		expectCode(t, err, InvalidPolicy)
	}
	// Rule order is semantic: normalization never sorts it.
	p = DefaultPolicy()
	p.Rules = []Rule{{ID: "z", Source: "s", Exclude: true}, {ID: "a", Source: "s", Exclude: true}}
	r = realRequest(t)
	r.Policy = p
	d, err = BuildDecision(r)
	if err != nil || !reflect.DeepEqual(d.Inputs.Policy.Rules, p.Rules) {
		t.Fatal(d, err)
	}
}

func TestCallerExclusionIdentityAndScopedRotation(t *testing.T) {
	r := fixture(t)
	restrict(&r, "alpha", "delta")
	r.Policy.Rules = []Rule{{ID: "caller-exclude", Source: "tb-custom", Prefer: &RulePreference{Families: []string{"one"}}}}
	r.Exclude = []string{"rt-a/alpha"}
	got := run(t, r)
	selected(t, got, "delta")
	if len(got.AppliedRules) != 2 || got.AppliedRules[0].ID == got.AppliedRules[1].ID {
		t.Fatal(got.AppliedRules)
	}
	// A reviewer-only standing rotation does not disable caller exclusions for developers.
	r.Policy.Rules = []Rule{{ID: "rotate", Source: "operator ruling: refusal rotation (example)", When: RuleWhen{Role: "reviewer"}, Exclude: true}}
	got = run(t, r)
	selected(t, got, "delta")
	if len(got.AppliedRules) != 1 || got.AppliedRules[0].Source != "--exclude" {
		t.Fatal(got.AppliedRules)
	}
}

func TestExampleHostReviewerAndStoryRulings(t *testing.T) {
	r := realRequest(t)
	r.Host = "build-host"
	r.Task.Role = "reviewer"
	r.Task.TaskClass = "review.code"
	r.ProducerFamily = "openai"
	r.Policy.Rules = []Rule{
		{ID: "host-review", Source: "operator ruling: host reviewer pin (example)", When: RuleWhen{Host: "build-host", Role: "reviewer"}, Require: &RuleSelector{Runtime: "codex", Model: "gpt-6-astra"}, Effort: &RuleEffort{Pin: "medium"}},
		{ID: "review", Source: "operator ruling: cross-provider review (example); exception operator ruling: host reviewer pin (example)", When: RuleWhen{Role: "reviewer"}, CrossProviderReview: &CrossProviderReview{AllowSameProviderFallback: true, ExceptHosts: []string{"build-host"}}},
	}
	got := run(t, r)
	if chosenExplanation(t, got).Candidate != (Candidate{"codex", "gpt-6-astra", "medium"}) || len(got.AppliedRules) != 2 {
		t.Fatal(got.RenderHuman())
	}
	r.Task.Difficulty = "hard"
	got = run(t, r)
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "operator ruling: host reviewer pin (example)") || got.Selected != nil {
		t.Fatal(got.RenderHuman())
	}
	r = realRequest(t)
	r.Story = "story-123"
	r.Policy.Rules = []Rule{{ID: "story", Source: "project-example/story-123", When: RuleWhen{Story: "story-123"}, Require: &RuleSelector{Runtime: "codex", Model: "gpt-6.1-sol"}, Effort: &RuleEffort{Pin: "high"}}}
	if chosenExplanation(t, run(t, r)).Candidate != (Candidate{"codex", "gpt-6.1-sol", "high"}) {
		t.Fatal("story ruling ignored")
	}
}

// Primary indices are task-specific; neither indices nor missing measurements cross scales.
func TestTaskQualityIndexMapping(t *testing.T) {
	row := CatalogRow{Quality: Quality{Overall: &QualityValue{Value: 70}, Coding: &QualityValue{Value: 60}}}
	for class, want := range map[string]float64{
		"code.implement": 60, "code.fix": 60, "tool-use": 60, "ops": 60,
		"research": 70, "docs": 70,
	} {
		if got := taskQuality(row, class); got == nil || got.Value != want {
			t.Fatalf("%s: got %v, want %v", class, got, want)
		}
	}
}
