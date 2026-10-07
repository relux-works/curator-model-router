package recommend

import (
	"cmp"
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

// Recommend computes a recommendation with a content id. No-qualified is a
// Recommendation.Refusal, not an error; malformed inputs return *Refusal errors.
func Recommend(in Request) (Recommendation, error) {
	d, err := BuildDecision(in)
	return d.Recommendation, err
}

// BuildDecision freezes independent copies of inputs and binds the result to
// their canonical content. Callers may persist JSON() and later call Replay.
func BuildDecision(in Request) (DecisionRecord, error) {
	in, err := normalizeRequest(in)
	if err != nil {
		return DecisionRecord{}, err
	}
	r, err := selectCandidates(in)
	if err != nil {
		return DecisionRecord{}, err
	}
	version := PublicSelectorVersion
	d := DecisionRecord{SchemaVersion: DecisionVersion, SelectorVersion: version, Inputs: in, Recommendation: r}
	id, err := d.ContentID()
	if err != nil {
		return DecisionRecord{}, err
	}
	d.DecisionID = id
	d.Recommendation.DecisionID = id
	return d, nil
}
func (d DecisionRecord) ContentID() (string, error) {
	d.DecisionID = ""
	d.Recommendation.DecisionID = ""
	return canonical.Digest(d)
}
func (d DecisionRecord) VerifyContentID() error {
	if d.SchemaVersion != DecisionVersion || (d.SelectorVersion != SelectorVersion && d.SelectorVersion != BudgetSelectorVersion && d.SelectorVersion != LegacySelectorVersion) {
		return refuse(DecisionMismatch, "unsupported decision or selector version")
	}
	id, err := d.ContentID()
	if err != nil {
		return err
	}
	if id != d.DecisionID || id != d.Recommendation.DecisionID {
		return refuse(DecisionMismatch, "decision content id differs")
	}
	return nil
}
func LoadDecision(raw []byte) (DecisionRecord, error) {
	var d DecisionRecord
	if err := strict(raw, &d, InvalidInput); err != nil {
		return d, err
	}
	if err := d.VerifyContentID(); err != nil {
		return d, err
	}
	return d, Replay(d)
}

// Replay verifies identity and recomputes the entire result from frozen inputs.
// A correctly rehashed but substituted recommendation is also refused.
func Replay(d DecisionRecord) error {
	if err := d.VerifyContentID(); err != nil {
		return err
	}
	rebuilt, err := BuildDecision(d.Inputs)
	if err != nil {
		return err
	}
	want, err := rebuilt.JSON()
	if err != nil {
		return err
	}
	got, err := d.JSON()
	if err != nil {
		return err
	}
	if string(want) != string(got) {
		return refuse(DecisionMismatch, "replay differs from recorded decision")
	}
	return nil
}

func normalizeRequest(in Request) (Request, error) {
	// A wholly absent policy is the missing-file case. Partial programmatic
	// policies must start from DefaultPolicy to preserve explicit zero values.
	if reflect.DeepEqual(in.Policy, Policy{}) {
		in.Policy = DefaultPolicy()
	}
	if in.CatalogOverlay != nil {
		digest, err := in.CatalogOverlay.Digest()
		if err != nil {
			return in, err
		}
		if in.OverlayDigest != "" && in.OverlayDigest != digest {
			return in, refuse(InvalidOverlay, "overlay digest differs")
		}
		in.OverlayDigest = digest
		merged, err := MergeCatalog(in.Catalog, *in.CatalogOverlay)
		if err != nil {
			return in, err
		}
		in.Catalog = merged
	} else if in.OverlayDigest != "" {
		return in, refuse(InvalidOverlay, "digest without overlay")
	}
	if err := in.Catalog.Validate(); err != nil {
		return in, err
	}
	if err := in.Policy.Validate(); err != nil {
		return in, err
	}
	if !validText(reflect.ValueOf(in)) {
		return in, refuse(InvalidInput, "invalid UTF-8 in input")
	}
	// Marshal/unmarshal detaches all pointers, nested maps and slices without
	// mutating caller values. Canonical validation follows normalization.
	raw, err := json.Marshal(in)
	if err != nil {
		return Request{}, refuse(InvalidInput, err.Error())
	}
	var copied Request
	if err = json.Unmarshal(raw, &copied); err != nil {
		return Request{}, refuse(InvalidInput, err.Error())
	}
	in = copied
	if in.Host == "" {
		in.Host = in.Policy.Host
	}
	in.Exclude = sortedSet(in.Exclude)
	for _, pair := range in.Exclude {
		parts := strings.Split(pair, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return in, refuse(InvalidInput, "exclude must contain runtime/model pairs")
		}
	}
	in.Catalog = normalizeCatalog(in.Catalog)
	if err = in.Catalog.Validate(); err != nil {
		return in, err
	}
	if err = in.Policy.Validate(); err != nil {
		return in, err
	}
	in.Task, err = in.Task.Normalize()
	if err != nil {
		return in, err
	}
	if in.AdmissionSource == "" {
		in.AdmissionSource = "caller"
	}
	if in.Candidates == nil {
		in.Candidates = []Candidate{}
	}
	slices.SortFunc(in.Candidates, func(a, b Candidate) int { return strings.Compare(a.Key(), b.Key()) })
	for i, c := range in.Candidates {
		if !validCandidate(c) {
			return in, refuse(InvalidInput, "admitted configuration is incomplete")
		}
		if i > 0 && c == in.Candidates[i-1] {
			return in, refuse(InvalidInput, "duplicate admitted configuration")
		}
	}
	u := &in.Usage
	if u.ScopeMap.Version == "" {
		u.ScopeMap.Version = "v1"
	}
	if u.ScopeMap.Entries == nil {
		u.ScopeMap.Entries = []routing.ScopeMapEntry{}
	}
	if u.Facts == nil {
		u.Facts = []routing.UsageFact{}
	}
	if u.Inflight == nil {
		u.Inflight = []routing.Inflight{}
	}
	slices.SortFunc(u.Facts, func(a, b routing.UsageFact) int { return strings.Compare(a.Key, b.Key) })
	for i := range u.Facts {
		if u.Facts[i].Windows == nil {
			u.Facts[i].Windows = []routing.UsageWindow{}
		}
		slices.SortFunc(u.Facts[i].Windows, func(a, b routing.UsageWindow) int { return strings.Compare(a.ID, b.ID) })
	}
	slices.SortFunc(u.Inflight, func(a, b routing.Inflight) int { return strings.Compare(a.Key, b.Key) })
	slices.SortFunc(u.ScopeMap.Entries, func(a, b routing.ScopeMapEntry) int {
		if c := strings.Compare(a.Runtime, b.Runtime); c != 0 {
			return c
		}
		return strings.Compare(a.Scope, b.Scope)
	})
	for i := range u.ScopeMap.Entries {
		slices.Sort(u.ScopeMap.Entries[i].ModelIDs)
	}
	for _, key := range u.UsageKeys {
		if key == "" {
			return in, refuse(InvalidInput, "usage_keys values must be nonempty")
		}
	}
	s := snapshot(in)
	if err = s.Validate(); err != nil {
		return in, refuse(InvalidInput, err.Error())
	}
	if _, err = canonical.MarshalRecord(in); err != nil {
		return in, refuse(InvalidInput, err.Error())
	}
	return in, nil
}

func selectCandidates(in Request) (Recommendation, error) {
	r := Recommendation{Alternatives: []RankedCandidate{}, FanOut: []Candidate{}, Explanation: []CandidateExplanation{}}
	admitted := map[string]bool{}
	for _, c := range in.Candidates {
		admitted[c.Key()] = true
	}
	catalogued := map[string]bool{}
	floor := in.Policy.Difficulties[in.Task.Difficulty].MinimumTier
	objective := in.Policy.Difficulties[in.Task.Difficulty].Objective
	if in.Task.Pipeline == "fanout" {
		floor = TierA
		objective = "quality"
	}
	if in.Task.Sensitivity == "delicate" {
		floor = TierS
		objective = "quality_ignore_cost"
	}
	budget := in.Policy.EffectiveBudgetMode()
	if budget != "balanced" {
		r.BudgetMode = budget
		if budget == "economy" {
			objective = "cheapest"
		} else {
			objective = "quality"
		}
	}
	for _, row := range in.Catalog.Rows {
		catalogued[row.Key()] = true
		q := taskQuality(row, in.Task.TaskClass)
		tierPolicy, tierQ := in.Policy, q
		index := qualityIndex(row, in.Task.TaskClass)
		if index == "review" || index == "bughunt_fallback" {
			tierPolicy.TierEdges = in.Policy.ReviewTierEdges
			tierQ = q
		}
		x := CandidateExplanation{RankedCandidate: RankedCandidate{Candidate: row.Candidate, Family: row.Family, Billing: row.Billing, Tier: TierFor(tierQ, tierPolicy), Quality: q, QualityIndex: index, Cost: row.Cost, ReasonCodes: []string{}}, Admitted: admitted[row.Key()]}
		if !x.Admitted {
			reason(&x, "not_admitted")
		}
		if (in.Locks.Agent != "" && in.Locks.Agent != row.Runtime) || (in.Locks.Model != "" && in.Locks.Model != row.Model) || (in.Locks.Effort != "" && in.Locks.Effort != row.Effort) {
			reason(&x, "lock_mismatch")
		}
		if row.Constraints.OnlyEffort != "" && row.Effort != row.Constraints.OnlyEffort {
			reason(&x, "constraint_only_effort")
		}
		for _, f := range row.Constraints.NotFor {
			if f == in.Task.Platform || f == in.Task.Language || f == in.Task.Role || f == in.Task.TaskClass || f == in.Task.Difficulty || f == in.Task.Sensitivity || f == in.Task.Pipeline {
				reason(&x, "constraint_not_for")
			}
		}
		if row.Billing == routing.BillingMetered && !in.Policy.AllowMetered {
			reason(&x, "metered_disabled")
		}
		if x.Tier == TierU {
			reason(&x, "quality_unknown")
		}
		x.Qualified = len(x.ReasonCodes) == 0
		// Informational provenance must not turn a fallback into a hard filter.
		if index == "bughunt_fallback" {
			reason(&x, "quality_from_bughunt_fallback")
		}
		if q != nil {
			reason(&x, "quality_from_"+index)
		}
		r.Explanation = append(r.Explanation, x)
	}
	for _, c := range in.Candidates {
		if !catalogued[c.Key()] {
			r.Explanation = append(r.Explanation, CandidateExplanation{RankedCandidate: RankedCandidate{Candidate: c, Tier: TierU, Cost: Cost{Kind: "estimate", Source: "unknown", AsOf: "unknown"}, ReasonCodes: []string{"catalog_missing"}}, Admitted: true})
		}
	}
	if applyRules(in, &r, floor) {
		return r, nil
	}
	// Delicate prefers S globally, then admits A only when no allowed S exists.
	if in.Task.Sensitivity == "delicate" {
		hasS := false
		for _, x := range r.Explanation {
			hasS = hasS || (x.Qualified && x.Tier == TierS)
		}
		if !hasS {
			floor = TierA
			for i := range r.Explanation {
				if r.Explanation[i].Qualified && tierRank(r.Explanation[i].Tier) >= tierRank(TierA) {
					reason(&r.Explanation[i], "delicate_a_fallback")
				}
			}
		}
	}
	bestBilling := 3
	for i := range r.Explanation {
		x := &r.Explanation[i]
		if x.Qualified && tierRank(x.Tier) < tierRank(floor) {
			x.Qualified = false
			reason(x, "below_minimum_tier")
		}
		if x.Qualified {
			bestBilling = min(bestBilling, billingRank(x.Billing))
		}
	}
	pool := []int{}
	for i := range r.Explanation {
		x := &r.Explanation[i]
		if !x.Qualified {
			continue
		}
		if billingRank(x.Billing) != bestBilling {
			x.Qualified = false
			reason(x, "billing_preference")
			continue
		}
		pool = append(pool, i)
		reason(x, "tier_qualified")
	}
	if len(pool) == 0 {
		r.Refusal = &Refusal{NoQualifiedCandidate, "no admitted configuration satisfies tier, billing, constraints and locks"}
		return r, nil
	}
	for _, i := range pool {
		x := &r.Explanation[i]
		x.Frontier = true
		for _, j := range pool {
			if i != j && dominates(r.Explanation[j].RankedCandidate, x.RankedCandidate) {
				x.Frontier = false
				break
			}
		}
		if x.Frontier {
			reason(x, "pareto_frontier")
		} else {
			reason(x, "pareto_dominated")
		}
	}
	qualityObjective := objective == "quality" || objective == "quality_ignore_cost"
	slices.SortFunc(pool, func(i, j int) int {
		a, b := r.Explanation[i], r.Explanation[j]
		if c := qualitySourceCompare(a.RankedCandidate, b.RankedCandidate); c != 0 {
			return c
		}
		if qualityObjective {
			if c := rankedQualityCompare(a.RankedCandidate, b.RankedCandidate); c != 0 {
				return c
			}
			if objective != "quality_ignore_cost" {
				if c := costCompare(a.Cost, b.Cost); c != 0 {
					return c
				}
			}
		} else {
			if a.Frontier != b.Frontier {
				if a.Frontier {
					return -1
				}
				return 1
			}
			if c := costCompare(a.Cost, b.Cost); c != 0 {
				return c
			}
			if c := rankedQualityCompare(a.RankedCandidate, b.RankedCandidate); c != 0 {
				return c
			}
		}
		return strings.Compare(a.Candidate.Key(), b.Candidate.Key())
	})
	if qualityObjective && strings.HasPrefix(in.Task.TaskClass, "review.") {
		pool = orderReviewQuality(pool, &r)
	}
	if objective == "standard" && r.Explanation[pool[0]].Tier == TierB {
		base := r.Explanation[pool[0]]
		for pos, i := range pool {
			x := r.Explanation[i]
			if tierRank(x.Tier) >= tierRank(TierA) && x.Frontier && x.QualityIndex == base.QualityIndex && costWithinRatio(x.Cost, base.Cost, in.Policy.StandardAMaxCostRatio) {
				copy(pool[1:pos+1], pool[:pos])
				pool[0] = i
				reason(&r.Explanation[i], "standard_a_value")
				break
			}
		}
	}
	reason(&r.Explanation[pool[0]], "objective_"+objective)
	// Derive disjoint anchored groups; do not chain ±25% neighbours into a
	// wider group. Quality-maximising objectives retain their maximum quality.
	groups := [][]int{}
	for _, i := range pool {
		x := r.Explanation[i]
		if x.Billing != routing.BillingSubscription {
			continue
		}
		joined := false
		for g, group := range groups {
			fits := true
			for _, j := range group {
				y := r.Explanation[j]
				fits = fits && budget == "balanced" && x.QualityIndex == y.QualityIndex && x.Tier == y.Tier && interchangeable(x.Cost, y.Cost, in.Policy.InterchangeableCostBP) && (!qualityObjective || rankedQualityCompare(x.RankedCandidate, y.RankedCandidate) == 0)
				// Review quality ties have a prescribed cost/configuration order.
				if qualityObjective && strings.HasPrefix(in.Task.TaskClass, "review.") {
					fits = false
				}
			}
			if fits {
				groups[g] = append(groups[g], i)
				joined = true
				break
			}
		}
		if !joined {
			groups = append(groups, []int{i})
		}
	}
	ordered, abstention, err := applyHeadroom(in, &r, pool, groups)
	if err != nil {
		return r, err
	}
	if abstention != nil {
		r.Refusal = &Refusal{NoQualifiedCandidate, string(abstention.Reason)}
		return r, nil
	}
	pool = ordered
	applySourcePreferences(in, &r, pool)
	if qualityObjective && strings.HasPrefix(in.Task.TaskClass, "review.") {
		// Hard rules already filtered admission. Preserve the measured review quality
		// order before matching scoped reviewer preferences break quality ties.
		pool = orderReviewQuality(pool, &r)
	}
	if budget == "burn" {
		pool = orderBurnQuality(pool, &r, in.Task.TaskClass)
		// Preserve non-review standing preferences inside qualified tiers.
		if !strings.HasPrefix(in.Task.TaskClass, "review.") {
			applySourcePreferences(in, &r, pool)
		}
	}
	if qualityObjective && strings.HasPrefix(in.Task.TaskClass, "review.") && highStakesReview(in) {
		pool = orderPreferredReview(in, &r, pool)
	}
	chosen := map[int]bool{}
	if in.Task.Pipeline == "fanout" {
		families := map[string]bool{}
		for _, i := range pool {
			x := &r.Explanation[i]
			if families[x.Family] {
				reason(x, "fanout_duplicate_family")
				continue
			}
			cap := in.Policy.FanOutK
			if budget == "burn" {
				cap = in.Policy.EffectiveBurnFanOutCap()
			}
			if len(r.FanOut) >= cap {
				reason(x, "fanout_limit")
				continue
			}
			families[x.Family] = true
			chosen[i] = true
			r.FanOut = append(r.FanOut, x.Candidate)
			reason(x, "fanout_distinct_family")
		}
	} else {
		chosen[pool[0]] = true
	}
	first := r.Explanation[pool[0]].Candidate
	r.Selected = &first
	for _, i := range pool {
		x := &r.Explanation[i]
		x.Selected = chosen[i]
		if x.Selected {
			reason(x, "selected")
		} else {
			reason(x, "ranked_alternative")
		}
		if x.Candidate != first {
			r.Alternatives = append(r.Alternatives, x.RankedCandidate)
		}
	}
	return r, nil
}
func reason(x *CandidateExplanation, code string) {
	x.ReasonCodes = append(x.ReasonCodes, code)
	slices.Sort(x.ReasonCodes)
	x.ReasonCodes = slices.Compact(x.ReasonCodes)
}
func taskQuality(r CatalogRow, class string) *QualityValue {
	if strings.HasPrefix(class, "review.") {
		return r.Quality.Review
	}
	if strings.HasPrefix(class, "code.") || class == "tool-use" || class == "ops" {
		if r.Quality.Coding != nil {
			return r.Quality.Coding
		}
	} else if r.Quality.Overall != nil {
		return r.Quality.Overall
	}
	return r.Quality.Review
}
func qualityIndex(r CatalogRow, class string) string {
	q := taskQuality(r, class)
	if q == nil {
		return "unknown"
	}
	if q == r.Quality.Review {
		if strings.HasPrefix(class, "review.") {
			return "review"
		}
		return "bughunt_fallback"
	}
	if q == r.Quality.Coding {
		return "coding"
	}
	return "overall"
}
func qualitySourceCompare(a, b RankedCandidate) int {
	rank := func(index string) int {
		if index == "unknown" {
			return 2
		}
		if index == "bughunt_fallback" {
			return 1
		}
		return 0
	}
	if c := cmp.Compare(rank(a.QualityIndex), rank(b.QualityIndex)); c != 0 {
		return c
	}
	// Different primary scales never arise for one task, but keep this helper
	// safe for callers/tests that construct mixed populations.
	return strings.Compare(a.QualityIndex, b.QualityIndex)
}
func applySourcePreferences(in Request, r *Recommendation, pool []int) {
	for start := 0; start < len(pool); {
		end := start + 1
		for end < len(pool) && r.Explanation[pool[start]].QualityIndex == r.Explanation[pool[end]].QualityIndex {
			end++
		}
		applyPreferences(in, r, pool[start:end])
		start = end
	}
}

// Noise ties are not transitive. Build bands against the highest remaining
// mean, then sort each band by cost and canonical configuration key. Never
// feed a non-transitive pairwise noise comparison to a sorting algorithm.
func orderReviewQuality(pool []int, r *Recommendation) []int {
	return orderReviewQualityBy(pool, r, func(i, j int) int {
		a, b := r.Explanation[i], r.Explanation[j]
		if c := costCompare(a.Cost, b.Cost); c != 0 {
			return c
		}
		return strings.Compare(a.Candidate.Key(), b.Candidate.Key())
	})
}

func orderReviewQualityBy(pool []int, r *Recommendation, compare func(i, j int) int) []int {
	remaining := slices.Clone(pool)
	slices.SortFunc(remaining, func(i, j int) int {
		if c := rankedQualityCompare(r.Explanation[i].RankedCandidate, r.Explanation[j].RankedCandidate); c != 0 {
			return c
		}
		return strings.Compare(r.Explanation[i].Candidate.Key(), r.Explanation[j].Candidate.Key())
	})
	ordered := make([]int, 0, len(pool))
	for len(remaining) > 0 {
		anchor := remaining[0]
		band, rest := []int{anchor}, []int{}
		for _, i := range remaining[1:] {
			if reviewNoiseTie(r.Explanation[anchor].RankedCandidate, r.Explanation[i].RankedCandidate) {
				band = append(band, i)
			} else {
				rest = append(rest, i)
			}
		}
		slices.SortFunc(band, compare)
		if len(band) > 1 {
			for _, i := range band {
				reason(&r.Explanation[i], "review_quality_noise_tie")
			}
		}
		ordered = append(ordered, band...)
		remaining = rest
	}
	return ordered
}
func reviewNoiseTie(a, b RankedCandidate) bool {
	if a.QualityIndex != b.QualityIndex {
		return false
	}
	if a.QualityIndex != "review" || b.QualityIndex != "review" {
		return qualityCompare(a.Quality, b.Quality) == 0
	}
	return a.Quality.Value == b.Quality.Value || math.Abs(a.Quality.Value-b.Quality.Value) < max(*a.Quality.Stderr, *b.Quality.Stderr)
}
func billingRank(b routing.BillingClass) int {
	switch b {
	case routing.BillingSubscription:
		return 0
	case routing.BillingLocal:
		return 1
	default:
		return 2
	}
}
func qualityCompare(a, b *QualityValue) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return 1
	}
	if b == nil {
		return -1
	}
	return cmp.Compare(b.Value, a.Value)
}

// Source groups precede numeric quality. Values are compared only on one scale.
func rankedQualityCompare(a, b RankedCandidate) int {
	if c := qualitySourceCompare(a, b); c != 0 {
		return c
	}
	return qualityCompare(a.Quality, b.Quality)
}
func costAxis(c Cost) (string, float64) {
	if c.USDPerTask != nil {
		return "usd", *c.USDPerTask
	}
	if c.TokensPerTask != nil {
		return "tokens", float64(*c.TokensPerTask)
	}
	return "unknown", 0
}
func costCompare(a, b Cost) int {
	// Compare token-only integer costs exactly (including values above 2^53).
	au, av := costAxis(a)
	bu, bv := costAxis(b)
	if au != bu {
		order := map[string]int{"usd": 0, "tokens": 1, "unknown": 2}
		return cmp.Compare(order[au], order[bu])
	}
	if au == "tokens" {
		return cmp.Compare(*a.TokensPerTask, *b.TokensPerTask)
	}
	return cmp.Compare(av, bv)
}
func costWithinRatio(a, b Cost, ratio float64) bool {
	au, _ := costAxis(a)
	bu, _ := costAxis(b)
	return au != "unknown" && au == bu && costRatio(a, b, new(big.Rat).SetFloat64(ratio))
}
func interchangeable(a, b Cost, bp int64) bool {
	au, _ := costAxis(a)
	bu, _ := costAxis(b)
	ratio := new(big.Rat).SetFrac64(10000+bp, 10000)
	return au != "unknown" && au == bu && costRatio(a, b, ratio) && costRatio(b, a, ratio)
}
func costRatio(a, b Cost, ratio *big.Rat) bool {
	amount := func(c Cost) *big.Rat {
		if c.USDPerTask != nil {
			return new(big.Rat).SetFloat64(*c.USDPerTask)
		}
		return new(big.Rat).SetInt64(*c.TokensPerTask)
	}
	return amount(a).Cmp(new(big.Rat).Mul(amount(b), ratio)) <= 0
}
func dominates(a, b RankedCandidate) bool {
	au, _ := costAxis(a.Cost)
	bu, _ := costAxis(b.Cost)
	if a.Quality == nil || b.Quality == nil || au == "unknown" || au != bu {
		return false
	}
	q, c := rankedQualityCompare(a, b), costCompare(a.Cost, b.Cost)
	return q <= 0 && c <= 0 && (q < 0 || c < 0)
}

func snapshot(in Request) routing.CandidateSnapshot {
	return routing.CandidateSnapshot{SchemaVersion: "v1", AsOf: in.Usage.AsOf, ScopeMap: in.Usage.ScopeMap, UsageFacts: in.Usage.Facts, Inflight: in.Usage.Inflight, Candidates: []routing.ExecutionCandidate{}, ConfigOrder: []string{}, Source: routing.Provenance{Name: in.AdmissionSource}}
}
func applyHeadroom(in Request, out *Recommendation, pool []int, groups [][]int) ([]int, *routing.Abstention, error) {
	s := snapshot(in)
	p := routing.DefaultPolicy("v1")
	p.Mode = routing.ModeSelect
	p.Headroom = in.Policy.Headroom
	p.Headroom.Equivalence = routing.DeclaredGroups
	p.Headroom.Groups = [][]routing.EquivalentPair{}
	for _, group := range groups {
		pairs := []routing.EquivalentPair{}
		for _, i := range group {
			c := out.Explanation[i].Candidate
			rt := c.Runtime
			pairs = append(pairs, routing.EquivalentPair{Runtime: &rt, Model: c.Model, Effort: c.Effort})
		}
		p.Headroom.Groups = append(p.Headroom.Groups, pairs)
	}
	indices := map[string]int{}
	for _, i := range pool {
		x := out.Explanation[i]
		c := x.Candidate
		id := c.Key()
		indices[id] = i
		var usageKey *string
		if x.Billing == routing.BillingSubscription {
			key, ok := in.Usage.UsageKeys[id]
			if !ok {
				key = "recommend-unknown:" + id
				// Ensure synthetic unknown identities never match supplied facts.
				for {
					found := false
					for _, f := range s.UsageFacts {
						found = found || f.Key == key
					}
					for _, n := range s.Inflight {
						found = found || n.Key == key
					}
					if !found {
						break
					}
					key += ":unknown"
				}
			}
			usageKey = &key
		}
		s.ConfigOrder = append(s.ConfigOrder, id)
		s.Candidates = append(s.Candidates, routing.ExecutionCandidate{ID: id, RuntimeBindingID: id, Runtime: c.Runtime, Model: c.Model, Effort: c.Effort, ContextProfile: routing.ContextProfile{Name: "recommend", LockSHA256: routing.Unknown}, Transport: "unknown", ProviderID: x.Family, ToolProfile: "unknown", ExecutionMode: "unknown", BillingClass: x.Billing, UsageKey: usageKey})
	}
	slices.SortFunc(s.Candidates, func(a, b routing.ExecutionCandidate) int { return strings.Compare(a.ID, b.ID) })
	digest, err := canonical.Digest(in.Catalog)
	if err != nil {
		return nil, nil, err
	}
	e := routing.EvaluationSnapshot{SchemaVersion: "v1", EvidenceSnapshotDigest: digest, Measurements: []string{}, Assessments: []routing.Assessment{}, Estimates: []routing.Estimate{}, EvaluatorVersions: []routing.Provenance{}}
	t := routing.TaskEnvelope{SchemaVersion: "v1", Description: in.Task.TaskClass, SubstantiveRevision: SelectorVersion, Role: in.Task.Role, Criteria: []string{}, Context: []string{}, Tools: []string{}}
	ex, err := routing.Explain(routing.DecisionBundle{SchemaVersion: "v1", Envelope: t, Snapshot: s, Evaluation: e, Policy: p, Versions: routing.DecisionOptions{SelectorVersion: routing.SelectorVersion, EstimatorVersion: routing.Unknown}})
	if err != nil {
		return nil, nil, refuse(InvalidInput, err.Error())
	}
	ordered := slices.Clone(pool)
	for _, h := range ex.Candidates {
		i := indices[h.CandidateID]
		copyH := h
		out.Explanation[i].Headroom = &copyH
		for _, code := range h.ReasonCodes {
			reason(&out.Explanation[i], string(code))
		}
		if h.FinalPosition != nil {
			ordered[int(*h.FinalPosition)] = i
		}
	}
	return ordered, ex.Abstention, nil
}
