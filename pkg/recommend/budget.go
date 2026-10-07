package recommend

import (
	"cmp"
	"slices"
	"strings"
)

func effectiveBudgetMode(mode string) string {
	if mode == "" {
		return "balanced"
	}
	return mode
}

// EffectiveBudgetMode includes the balanced default of legacy policies.
func (p Policy) EffectiveBudgetMode() string { return effectiveBudgetMode(p.BudgetMode) }

// EffectiveBurnFanOutCap defaults to four without changing legacy wire bytes.
func (p Policy) EffectiveBurnFanOutCap() int {
	if p.BurnFanOutCap == 0 {
		return 4
	}
	return p.BurnFanOutCap
}

func highStakesReview(in Request) bool {
	return in.Task.Role == "reviewer" && (in.Task.Difficulty == "hard" || in.Task.Difficulty == "critical" || in.Task.Sensitivity == "delicate")
}

// Burn retains anchored quality bands, but spends the largest remaining known
// subscription quota inside a tie before applying the cost/key tie breakers.
// Missing/stale usage is neutral and never treated as spare capacity.
func orderBurnQuality(pool []int, out *Recommendation, class string) []int {
	remaining := slices.Clone(pool)
	slices.SortFunc(remaining, func(i, j int) int {
		a, b := out.Explanation[i], out.Explanation[j]
		if c := rankedQualityCompare(a.RankedCandidate, b.RankedCandidate); c != 0 {
			return c
		}
		return strings.Compare(a.Candidate.Key(), b.Candidate.Key())
	})
	ordered := make([]int, 0, len(pool))
	for len(remaining) > 0 {
		anchor := remaining[0]
		band, rest := []int{anchor}, []int{}
		for _, i := range remaining[1:] {
			a, b := out.Explanation[anchor].RankedCandidate, out.Explanation[i].RankedCandidate
			tie := rankedQualityCompare(a, b) == 0
			if strings.HasPrefix(class, "review.") {
				tie = reviewNoiseTie(a, b)
			}
			if tie {
				band = append(band, i)
			} else {
				rest = append(rest, i)
			}
		}
		slices.SortFunc(band, func(i, j int) int {
			return burnTieCompare(out.Explanation[i], out.Explanation[j])
		})
		ordered = append(ordered, band...)
		remaining = rest
	}
	return ordered
}

func burnTieCompare(a, b CandidateExplanation) int {
	headroom := func(x CandidateExplanation) int64 {
		if x.Headroom == nil || x.Headroom.Headroom == nil {
			return -1
		}
		return *x.Headroom.Headroom
	}
	if c := cmp.Compare(headroom(b), headroom(a)); c != 0 {
		return c
	}
	if c := costCompare(a.Cost, b.Cost); c != 0 {
		return c
	}
	return strings.Compare(a.Candidate.Key(), b.Candidate.Key())
}
