"""Independent synthetic acceptance arithmetic; no W2/estimator imports.
Run before authoring goldens. Normal Go checks only compare frozen goldens.
"""
from fractions import Fraction


def nearest_even(value):
    lower = value.numerator // value.denominator
    remainder = value - lower
    return lower + (remainder > Fraction(1, 2) or
                    remainder == Fraction(1, 2) and lower % 2 == 1)


fitness_cases = {
    "direct": ("0.8", 9000),
    "partial": ("0.4", 7000),
    "zero": ("0", 5000),
    "scoped-note": ("0.6", 8000),
    "partial-note": ("0.3", 6500),
    "medium-partial-note": ("0.18", 5900),
    "weakness": ("-0.3", 3500),
    "measurement-plus-note": ("0.7", 8500),
    "half-even-down": ("0.2001", 6000),
    "half-even-up": ("0.2003", 6002),
    "negative-one": ("-1", 0),
}
for name, (utility, expected) in fitness_cases.items():
    actual = nearest_even(5000 * (1 + Fraction(utility)))
    assert actual == expected, (name, actual, expected)

coverage_cases = [
    (10000, 10000, "1", 10000),
    (10000, 10000, "0.5", 5000),
    (10000, 7500, "1", 7500),
    (10000, 5000, "1", 5000),
    (10000, 5000, "0.5", 2500),
    (7500, 10000, "1", 7500),
    (7500, 10000, "0.5", 3750),
    (25, 10000, "0.5", 12),
]
for weight, coefficient, match, expected in coverage_cases:
    actual = nearest_even(Fraction(weight * coefficient, 10000) * Fraction(match))
    assert actual == expected, (weight, coefficient, match, actual, expected)

round_once_cases = {
    "mixed-kinds": ([(1, 10000, "0.5"), (9999, 7500, "1")], 7500, 7499),
    "two-half-ties": ([(25, 10000, "0.5"), (25, 10000, "0.5"),
                        (9950, 0, "0")], 25, 24),
}
for name, (categories, expected, premature) in round_once_cases.items():
    terms = [Fraction(weight * coefficient, 10000) * Fraction(match)
             for weight, coefficient, match in categories]
    actual = nearest_even(sum(terms, Fraction(0)))
    rounded_separately = sum(nearest_even(term) for term in terms)
    assert actual == expected, (name, actual, expected)
    assert rounded_separately == premature, (name, rounded_separately, premature)
    assert actual != rounded_separately, name

print("Exact independent oracle: 11 fitness, 8 coverage and 2 round-once expectations passed.")
