package evidence

import (
	"encoding/json"
	"math/big"
	"sort"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

// Project decimal tokens from the canonical W2 view, never from binary64
// multiplication. W2 derivation itself remains unchanged and pinned by digest.
func (e *ViewEstimator) project() ([]routing.Estimate, error) {
	raw, err := canonical.Marshal(e.view)
	if err != nil {
		return nil, err
	}
	var view struct {
		Derivation struct {
			PartialDiscount json.Number `json:"partial_discount"`
		} `json:"derivation"`
		Results []struct {
			Score *json.Number `json:"score,omitempty"`
			Scale struct {
				Min json.Number `json:"min"`
				Max json.Number `json:"max"`
			} `json:"scale"`
			Contributions []struct {
				Ref       string      `json:"ref"`
				Direction string      `json:"direction"`
				Weight    json.Number `json:"weight"`
				Stale     bool        `json:"stale"`
			} `json:"contributions"`
			Coverage []Coverage `json:"coverage"`
		} `json:"results"`
	}
	if err = json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}
	discount, err := estimatorRational(view.Derivation.PartialDiscount)
	if err != nil {
		return nil, err
	}
	out := make([]routing.Estimate, 0, len(e.input.Candidates))
	projected := map[string]routing.Estimate{}
	tolerance := big.NewRat(1, 1000000000000)
	for i, result := range view.Results {
		lo, err := estimatorRational(result.Scale.Min)
		if err != nil {
			return nil, err
		}
		hi, err := estimatorRational(result.Scale.Max)
		if err != nil {
			return nil, err
		}
		if lo.Cmp(big.NewRat(-1, 1)) != 0 || hi.Cmp(big.NewRat(1, 1)) != 0 {
			return nil, refuse(EstimatorViewMismatch, "view scale must be [-1,1]")
		}
		utility := new(big.Rat)
		refs := map[string]bool{}
		for _, c := range result.Contributions {
			w, err := estimatorRational(c.Weight)
			if err != nil {
				return nil, err
			}
			if w.Sign() < 0 || !member(c.Direction, "+", "-") {
				return nil, refuse(EstimatorViewMismatch, "invalid signed contribution")
			}
			if c.Direction == "-" {
				utility.Sub(utility, w)
			} else {
				utility.Add(utility, w)
			}
			if !c.Stale && (!strings.HasPrefix(c.Ref, "note:") || w.Sign() > 0) {
				refs[c.Ref] = true
			}
		}
		estimate := routing.Estimate{EstimatorVersion: EstimatorVersion, ContributingRecordIDs: []string{}}
		for id := range refs {
			estimate.ContributingRecordIDs = append(estimate.ContributingRecordIDs, id)
		}
		sort.Strings(estimate.ContributingRecordIDs)
		if result.Score != nil {
			score, err := estimatorRational(*result.Score)
			if err != nil {
				return nil, err
			}
			delta := new(big.Rat).Sub(utility, score)
			delta.Abs(delta)
			if delta.Cmp(tolerance) > 0 {
				return nil, refuse(EstimatorViewMismatch, "exact contributions disagree with displayed score")
			}
			// Clamp only a tolerance-sized binary64 overshoot at the utility boundary.
			for _, x := range []*big.Rat{utility, score} {
				bound := hi
				if x.Sign() < 0 {
					bound = lo
				}
				if x.Cmp(lo) < 0 || x.Cmp(hi) > 0 {
					overshoot := new(big.Rat).Sub(x, bound)
					overshoot.Abs(overshoot)
					if overshoot.Cmp(tolerance) > 0 {
						return nil, refuse(EstimatorViewMismatch, "utility outside verified scale")
					}
					x.Set(bound)
				}
			}
			fitness := new(big.Rat).Add(utility, big.NewRat(1, 1))
			fitness.Mul(fitness, big.NewRat(5000, 1))
			bp, err := estimatorRoundBP(fitness)
			if err != nil {
				return nil, err
			}
			estimate.Fitness = &bp
		} else if utility.Sign() != 0 || len(refs) != 0 {
			return nil, refuse(EstimatorViewMismatch, "absent score contradicts usable contributions")
		}
		coverage := new(big.Rat)
		for j, c := range result.Coverage {
			coefficient := int64(0)
			switch c.Kind {
			case "measured":
				coefficient = 10000
			case "interpolated":
				coefficient = 7500
			case "transferred":
				coefficient = 5000
			case "notes-only", "none":
			default:
				return nil, refuse(EstimatorViewMismatch, "unknown coverage kind")
			}
			factor := new(big.Rat)
			switch c.Match {
			case "direct":
				factor.SetInt64(1)
			case "partial":
				factor.Set(discount)
			case "none":
			default:
				return nil, refuse(EstimatorViewMismatch, "unknown coverage match")
			}
			w := *e.requirements.Items[j].WeightBP
			term := new(big.Rat).Mul(big.NewRat(w*coefficient, 10000), factor)
			coverage.Add(coverage, term)
		}
		bp, err := estimatorRoundBP(coverage)
		if err != nil {
			return nil, err
		}
		estimate.Coverage = &bp
		projected[fingerprintKey(e.view.Candidates[i])] = estimate
	}
	for _, b := range e.input.Fingerprints {
		estimate := estimatorCopy(projected[fingerprintKey(b.Fingerprint)])
		estimate.CandidateID = b.CandidateID
		out = append(out, estimate)
	}
	return out, nil
}

func estimatorRational(n json.Number) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return nil, refuse(EstimatorViewMismatch, "invalid canonical decimal")
	}
	return r, nil
}

func estimatorRoundBP(r *big.Rat) (int64, error) {
	if r.Sign() < 0 || r.Cmp(big.NewRat(10000, 1)) > 0 {
		return 0, refuse(EstimatorViewMismatch, "projected value outside bp scale")
	}
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	cmp := new(big.Int).Lsh(rem, 1).Cmp(r.Denom())
	if cmp > 0 || cmp == 0 && q.Bit(0) == 1 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, refuse(EstimatorViewMismatch, "bp integer overflow")
	}
	return q.Int64(), nil
}
