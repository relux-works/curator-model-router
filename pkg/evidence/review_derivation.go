package evidence

// ReviewDerivationV2 is a new artifact. The default remains version 1.
func ReviewDerivationV2() Derivation {
	d := DefaultDerivation()
	d.Version = "2"
	d.Normalisations = append(d.Normalisations, Normalisation{Reference{"internal-review-set", "1"}, "review_f1", 0, 1, "higher_better", []string{"review.code"}})
	return d
}
