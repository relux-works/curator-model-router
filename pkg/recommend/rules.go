package recommend

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/routing"
)

// Rule is an ordered operator ruling. JSON and TOML share the same field names.
// Empty When fields match everything; populated fields are exact AND matches.
// Require filters the matched population; it never restores a forbidden row.
type Rule struct {
	ID                  string               `json:"id" toml:"id"`
	Source              string               `json:"source" toml:"source"`
	Rationale           string               `json:"rationale,omitempty" toml:"rationale"`
	When                RuleWhen             `json:"when" toml:"when"`
	Require             *RuleSelector        `json:"require,omitempty" toml:"require"`
	Forbid              *RuleSelector        `json:"forbid,omitempty" toml:"forbid"`
	Prefer              *RulePreference      `json:"prefer,omitempty" toml:"prefer"`
	Effort              *RuleEffort          `json:"effort,omitempty" toml:"effort"`
	QuotaStopBP         *int64               `json:"quota_stop_bp,omitempty" toml:"quota_stop_bp"`
	CrossProviderReview *CrossProviderReview `json:"cross_provider_review,omitempty" toml:"cross_provider_review"`
	Exclude             bool                 `json:"exclude,omitempty" toml:"exclude"`
}
type RuleWhen struct {
	Difficulty  []string `json:"difficulty,omitempty" toml:"difficulty"`
	Sensitivity []string `json:"sensitivity,omitempty" toml:"sensitivity"`
	Host        string   `json:"host,omitempty" toml:"host"`
	Role        string   `json:"role,omitempty" toml:"role"`
	TaskClass   string   `json:"task_class,omitempty" toml:"task_class"`
	Runtime     string   `json:"runtime,omitempty" toml:"runtime"`
	Model       string   `json:"model,omitempty" toml:"model"`
	Story       string   `json:"story,omitempty" toml:"story"`
}

// RuleSelector fields are conjunctive; omitted fields are unrestricted.
type RuleSelector struct {
	Runtime string `json:"runtime,omitempty" toml:"runtime"`
	Model   string `json:"model,omitempty" toml:"model"`
	Family  string `json:"family,omitempty" toml:"family"`
	Effort  string `json:"effort,omitempty" toml:"effort"`
}
type RulePreference struct {
	Runtimes []string `json:"runtimes,omitempty" toml:"runtimes"`
	Families []string `json:"families,omitempty" toml:"families"`
}

// RuleEffort uses none < low < medium < high < xhigh < max < ultra.
// Pin and range are mutually exclusive. Range endpoints are inclusive.
type RuleEffort struct {
	Pin string `json:"pin,omitempty" toml:"pin"`
	Min string `json:"min,omitempty" toml:"min"`
	Max string `json:"max,omitempty" toml:"max"`
}
type CrossProviderReview struct {
	AllowSameProviderFallback bool `json:"allow_same_provider_fallback,omitempty" toml:"allow_same_provider_fallback"`
	// ExceptHosts explicitly records operator exceptions, such as a reviewer pin on a build host.
	ExceptHosts []string `json:"except_hosts,omitempty" toml:"except_hosts"`
}
type AppliedRule struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Rationale string `json:"rationale,omitempty"`
}

type SkippedRule struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Facet  string `json:"facet"`
}

func validateRules(rules []Rule) error {
	seen := map[string]bool{}
	for _, r := range rules {
		bad := func(message string) error { return refuse(InvalidPolicy, fmt.Sprintf("rule %q: %s", r.ID, message)) }
		if r.ID == "" || r.Source == "" || seen[r.ID] {
			return bad("unique id and source are required")
		}
		seen[r.ID] = true
		for _, scope := range []struct {
			name            string
			values, allowed []string
		}{
			{"difficulty", r.When.Difficulty, []string{"trivial", "routine", "standard", "hard", "critical"}},
			{"sensitivity", r.When.Sensitivity, []string{"normal", "delicate"}},
		} {
			if scope.values != nil && len(scope.values) == 0 {
				return bad("empty " + scope.name + " scope")
			}
			seenValues := map[string]bool{}
			for _, v := range scope.values {
				if !slices.Contains(scope.allowed, v) || seenValues[v] {
					return bad("invalid or duplicate " + scope.name)
				}
				seenValues[v] = true
			}
		}
		if r.Require == nil && r.Forbid == nil && r.Prefer == nil && r.Effort == nil && r.QuotaStopBP == nil && r.CrossProviderReview == nil && !r.Exclude {
			return bad("an action is required")
		}
		for _, s := range []*RuleSelector{r.Require, r.Forbid} {
			if s != nil && *s == (RuleSelector{}) {
				return bad("empty require/forbid selector")
			}
		}
		if r.Prefer != nil {
			if len(r.Prefer.Runtimes)+len(r.Prefer.Families) == 0 {
				return bad("empty preference")
			}
			for _, list := range [][]string{r.Prefer.Runtimes, r.Prefer.Families} {
				seenValues := map[string]bool{}
				for _, v := range list {
					if v == "" || seenValues[v] {
						return bad("preference values must be nonempty and unique")
					}
					seenValues[v] = true
				}
			}
		}
		if e := r.Effort; e != nil {
			if e.Pin != "" {
				if e.Min != "" || e.Max != "" || effortRank(e.Pin) < 0 {
					return bad("invalid effort pin")
				}
			} else {
				if e.Min == "" && e.Max == "" || e.Min != "" && effortRank(e.Min) < 0 || e.Max != "" && effortRank(e.Max) < 0 || e.Min != "" && e.Max != "" && effortRank(e.Min) > effortRank(e.Max) {
					return bad("invalid effort range")
				}
			}
		}
		if r.QuotaStopBP != nil && (*r.QuotaStopBP < 0 || *r.QuotaStopBP > 10000) {
			return bad("quota_stop_bp must be 0..10000")
		}
		if r.CrossProviderReview != nil {
			for _, h := range r.CrossProviderReview.ExceptHosts {
				if h == "" {
					return bad("empty exception host")
				}
			}
		}
	}
	return nil
}
func effortRank(e string) int {
	return slices.Index([]string{"none", "low", "medium", "high", "xhigh", "max", "ultra"}, e)
}
func (w RuleWhen) contextMismatch(in Request) string {
	if w.Host != "" && w.Host != in.Host {
		return "host"
	}
	if w.Role != "" && w.Role != in.Task.Role {
		return "role"
	}
	if w.TaskClass != "" && w.TaskClass != in.Task.TaskClass {
		return "task_class"
	}
	if w.Story != "" && w.Story != in.Story {
		return "story"
	}
	if len(w.Difficulty) > 0 && !slices.Contains(w.Difficulty, in.Task.Difficulty) {
		return "difficulty"
	}
	if len(w.Sensitivity) > 0 && !slices.Contains(w.Sensitivity, in.Task.Sensitivity) {
		return "sensitivity"
	}
	return ""
}
func (w RuleWhen) contextMatches(in Request) bool {
	return w.contextMismatch(in) == ""
}
func (w RuleWhen) candidateMatches(c Candidate) bool {
	return (w.Runtime == "" || w.Runtime == c.Runtime) && (w.Model == "" || w.Model == c.Model)
}
func (s RuleSelector) matches(x CandidateExplanation) bool {
	return (s.Runtime == "" || s.Runtime == x.Candidate.Runtime) && (s.Model == "" || s.Model == x.Candidate.Model) && (s.Family == "" || s.Family == x.Family) && (s.Effort == "" || s.Effort == x.Candidate.Effort)
}
func (e RuleEffort) matches(effort string) bool {
	if e.Pin != "" {
		return effort == e.Pin
	}
	n := effortRank(effort)
	return n >= 0 && (e.Min == "" || n >= effortRank(e.Min)) && (e.Max == "" || n <= effortRank(e.Max))
}

// qualifyingFloor preserves the selector's explicit delicate S -> A fallback.
func qualifyingFloor(in Request, out *Recommendation, floor Tier) Tier {
	if in.Task.Sensitivity == "delicate" {
		for _, x := range out.Explanation {
			if x.Qualified && x.Tier == TierS {
				return TierS
			}
		}
		return TierA
	}
	return floor
}
func hasQualified(in Request, out *Recommendation, floor Tier) bool {
	floor = qualifyingFloor(in, out, floor)
	for _, x := range out.Explanation {
		if x.Qualified && tierRank(x.Tier) >= tierRank(floor) {
			return true
		}
	}
	return false
}
func applyRules(in Request, out *Recommendation, floor Tier) bool {
	rules := slices.Clone(in.Policy.Rules)
	// Caller exclusions are hard even without a standing rotation rule. When a
	// matching policy rule exists, its source explains the same exclusions.
	covered := false
	for _, r := range rules {
		covered = covered || r.Exclude && r.When.contextMatches(in) && r.When.Runtime == "" && r.When.Model == ""
	}
	if len(in.Exclude) > 0 && !covered {
		id := "caller-exclude"
		for {
			used := false
			for _, rule := range rules {
				used = used || rule.ID == id
			}
			if !used {
				break
			}
			id += "-caller"
		}
		rules = append(rules, Rule{ID: id, Source: "--exclude", Rationale: "Rotate away from caller-excluded runtime/model pairs.", Exclude: true})
	}
	for _, r := range rules {
		if facet := r.When.contextMismatch(in); facet != "" {
			// Legacy unscoped rules keep their exact explanation bytes.
			if len(r.When.Difficulty)+len(r.When.Sensitivity) > 0 {
				out.SkippedRules = append(out.SkippedRules, SkippedRule{r.ID, r.Source, facet})
			}
			continue
		}
		matched := false
		for _, x := range out.Explanation {
			matched = matched || x.Admitted && r.When.candidateMatches(x.Candidate)
		}
		if !matched && (r.When.Runtime != "" || r.When.Model != "") {
			continue
		}
		out.AppliedRules = append(out.AppliedRules, AppliedRule{r.ID, r.Source, r.Rationale})
		before := hasQualified(in, out, floor)
		// All ordinary hard actions run before computing this rule's review fallback.
		for i := range out.Explanation {
			x := &out.Explanation[i]
			if !x.Qualified || !r.When.candidateMatches(x.Candidate) {
				continue
			}
			action := ""
			switch {
			case r.Forbid != nil && r.Forbid.matches(*x):
				action = "forbid"
			case r.Require != nil && !r.Require.matches(*x):
				action = "require"
			case r.Effort != nil && !r.Effort.matches(x.Candidate.Effort):
				action = "effort"
			case r.QuotaStopBP != nil && quotaStopped(in, *x, *r.QuotaStopBP):
				action = "quota_stop"
			case r.Exclude && slices.Contains(in.Exclude, x.Candidate.Runtime+"/"+x.Candidate.Model):
				action = "exclude"
			}
			if action != "" {
				x.Qualified = false
				reason(x, "rule:"+r.ID+":"+action)
			}
		}
		if cross := r.CrossProviderReview; cross != nil && in.Task.Role == "reviewer" && in.ProducerFamily != "" {
			if slices.Contains(cross.ExceptHosts, in.Host) {
				for i := range out.Explanation {
					x := &out.Explanation[i]
					if x.Qualified && r.When.candidateMatches(x.Candidate) {
						reason(x, "rule:"+r.ID+":cross_provider_exception")
					}
				}
			} else {
				hasOther := false
				reviewFloor := qualifyingFloor(in, out, floor)
				for _, x := range out.Explanation {
					hasOther = hasOther || x.Qualified && tierRank(x.Tier) >= tierRank(reviewFloor) && r.When.candidateMatches(x.Candidate) && x.Family != in.ProducerFamily
				}
				for i := range out.Explanation {
					x := &out.Explanation[i]
					if !x.Qualified || !r.When.candidateMatches(x.Candidate) || x.Family != in.ProducerFamily {
						continue
					}
					if hasOther || !cross.AllowSameProviderFallback {
						x.Qualified = false
						reason(x, "rule:"+r.ID+":cross_provider_review")
					} else {
						reason(x, "rule:"+r.ID+":same_provider_fallback")
					}
				}
			}
		}
		if before && !hasQualified(in, out, floor) {
			out.Refusal = &Refusal{NoQualifiedCandidate, fmt.Sprintf("rule %s (source %s) leaves no qualified candidate", r.ID, r.Source)}
			return true
		}
	}
	return false
}

// quotaStopped takes the minimum of known applicable windows, allowing an
// unknown companion window to remain neutral. Freshness is caller-frozen, as
// in routing; the library never reads a clock or treats missing usage as zero.
func quotaStopped(in Request, x CandidateExplanation, stop int64) bool {
	if x.Billing != routing.BillingSubscription {
		return false
	}
	key, ok := in.Usage.UsageKeys[x.Candidate.Key()]
	if !ok {
		return false
	}
	for _, f := range in.Usage.Facts {
		if f.Key != key || f.Runtime != x.Candidate.Runtime || f.State == routing.StateAbsent || f.State == routing.StateUnavailable || f.State == routing.StateNotSupported {
			continue
		}
		for _, w := range f.Windows {
			mapped := w.Scope == "all"
			for _, e := range in.Usage.ScopeMap.Entries {
				if e.Runtime == x.Candidate.Runtime && e.Scope == w.Scope {
					mapped = mapped || slices.Contains(e.ModelIDs, x.Candidate.Model)
				}
			}
			if !mapped || !(in.Policy.Headroom.Windows.All || slices.Contains(in.Policy.Headroom.Windows.IDs, w.ID)) || w.Freshness != routing.Fresh || routing.ClassifyFreshness(w, in.Usage.AsOf, routing.MaxTTL) == routing.Invalid || w.ResetsAt != nil && *w.ResetsAt <= in.Usage.AsOf {
				continue
			}
			if max(int64(0), 10000-*w.UsedBP) <= stop {
				return true
			}
		}
	}
	return false
}

// Preferences permute each tier's existing slots. Earlier rules take priority;
// headroom and objective order remain the tie-breakers for equal preferences.
func applyPreferences(in Request, out *Recommendation, pool []int) {
	active := map[string]bool{}
	for _, r := range out.AppliedRules {
		active[r.ID] = true
	}
	tiers := []Tier{TierS, TierA, TierB, TierC}
	for _, tier := range tiers {
		positions, indices := []int{}, []int{}
		for pos, i := range pool {
			if out.Explanation[i].Tier == tier {
				positions = append(positions, pos)
				indices = append(indices, i)
			}
		}
		slices.SortStableFunc(indices, func(i, j int) int {
			return preferenceCompare(in, active, out.Explanation[i], out.Explanation[j], false)
		})
		for pos, i := range indices {
			pool[positions[pos]] = i
		}
	}
	for _, i := range pool {
		for _, r := range in.Policy.Rules {
			if r.Prefer != nil && active[r.ID] && r.When.candidateMatches(out.Explanation[i].Candidate) {
				reason(&out.Explanation[i], "rule:"+r.ID+":prefer")
			}
		}
	}
}

// Review quality ties consider only preferences with their own matching scope.
// Ordinary within-tier preferences retain their existing ordered-list semantics.
func preferenceCompare(in Request, active map[string]bool, a, b CandidateExplanation, scopedOnly bool) int {
	for _, r := range in.Policy.Rules {
		if r.Prefer == nil || !active[r.ID] || scopedOnly && len(r.When.Difficulty)+len(r.When.Sensitivity) == 0 {
			continue
		}
		rank := func(x CandidateExplanation, list []string, v string) int {
			if !r.When.candidateMatches(x.Candidate) {
				return len(list)
			}
			n := slices.Index(list, v)
			if n < 0 {
				return len(list)
			}
			return n
		}
		if c := cmp.Compare(rank(a, r.Prefer.Runtimes, a.Candidate.Runtime), rank(b, r.Prefer.Runtimes, b.Candidate.Runtime)); c != 0 {
			return c
		}
		if c := cmp.Compare(rank(a, r.Prefer.Families, a.Family), rank(b, r.Prefer.Families, b.Family)); c != 0 {
			return c
		}
	}
	return 0
}

func orderPreferredReview(in Request, out *Recommendation, pool []int) []int {
	active := map[string]bool{}
	for _, r := range out.AppliedRules {
		active[r.ID] = true
	}
	// Anchor bands on quality before consulting any preference. A matched
	// scoped preference can break ties, never promote a weaker quality band.
	return orderReviewQualityBy(pool, out, func(i, j int) int {
		a, b := out.Explanation[i], out.Explanation[j]
		if c := preferenceCompare(in, active, a, b, true); c != 0 {
			return c
		}
		if in.Policy.EffectiveBudgetMode() == "burn" {
			return burnTieCompare(a, b)
		}
		if c := costCompare(a.Cost, b.Cost); c != 0 {
			return c
		}
		return strings.Compare(a.Candidate.Key(), b.Candidate.Key())
	})
}
