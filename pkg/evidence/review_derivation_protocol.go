package evidence

import "math/big"

// ReviewCounts is the runner's adjudicated summary, not an imported observation.
// Case counts are independent of finding counts and the F1 denominator.
type ReviewCounts struct {
	TrueDefectsFound    int64 `json:"true_defects_found"`
	FalseFindings       int64 `json:"false_findings"`
	MissedDefects       int64 `json:"missed_defects"`
	ExpectedCases       int64 `json:"expected_cases"`
	EvaluatedCases      int64 `json:"evaluated_cases"`
	CorrectCleanReviews int64 `json:"correct_clean_reviews"`
}
type ReviewAggregate struct {
	Complete            bool     `json:"complete"`
	ReviewF1            *float64 `json:"review_f1,omitempty"`
	SampleCount         int64    `json:"sample_count"`
	CorrectCleanReviews int64    `json:"correct_clean_reviews"`
}

// AggregateReview uses the initial protocol's unweighted objective. Incomplete
// sets and zero denominators have no comparable F1, including clean controls.
func AggregateReview(c ReviewCounts) (ReviewAggregate, error) {
	if c.TrueDefectsFound < 0 || c.FalseFindings < 0 || c.MissedDefects < 0 || c.ExpectedCases <= 0 || c.EvaluatedCases < 0 || c.EvaluatedCases > c.ExpectedCases || c.CorrectCleanReviews < 0 || c.CorrectCleanReviews > c.EvaluatedCases {
		return ReviewAggregate{}, refuse("evidence_evalrun_invalid_result", "invalid adjudicated case summary")
	}
	a := ReviewAggregate{Complete: c.ExpectedCases == c.EvaluatedCases, SampleCount: c.EvaluatedCases, CorrectCleanReviews: c.CorrectCleanReviews}
	if !a.Complete {
		return a, nil
	}
	numerator := new(big.Int).Mul(big.NewInt(c.TrueDefectsFound), big.NewInt(2))
	denominator := new(big.Int).Add(new(big.Int).Set(numerator), big.NewInt(c.FalseFindings))
	denominator.Add(denominator, big.NewInt(c.MissedDefects))
	if denominator.Sign() != 0 {
		f, _ := new(big.Rat).SetFrac(numerator, denominator).Float64()
		a.ReviewF1 = &f
	}
	return a, nil
}
