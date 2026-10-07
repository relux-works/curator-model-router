package routing

import (
	"math/big"
	"strings"
)

const (
	MinTimestamp int64 = 946684800
	MaxTimestamp int64 = 7258118400
	MinTTL       int64 = 1
	MaxTTL       int64 = 86400
	MinMinutes   int64 = 1
	MaxMinutes   int64 = 527040
	MinUsedBP    int64 = 0
	MaxUsedBP    int64 = 1000000
	DefaultTTL   int64 = 600
)

func timestampValid(v int64) bool { return v >= MinTimestamp && v <= MaxTimestamp }

// ClassifyFreshness uses invalid > expired > fresh > stale precedence and reads
// no clock. TODO(decision): an out-of-range as_of or TTL yields invalid here;
// snapshot validation separately refuses an invalid as_of. An absent used_bp
// is also invalid rather than a fabricated zero. TTL is caller input,
// not a field added to the frozen projection.
func ClassifyFreshness(w UsageWindow, asOf, ttlS int64) Freshness {
	if !timestampValid(asOf) || ttlS < MinTTL || ttlS > MaxTTL || w.Minutes < MinMinutes || w.Minutes > MaxMinutes || w.UsedBP == nil || *w.UsedBP < MinUsedBP || *w.UsedBP > MaxUsedBP || w.ObservedAt == nil || !timestampValid(*w.ObservedAt) || *w.ObservedAt > asOf || (w.ResetsAt != nil && !timestampValid(*w.ResetsAt)) {
		return Invalid
	}
	if w.ResetsAt != nil && *w.ResetsAt <= asOf {
		return Expired
	}
	if asOf-*w.ObservedAt <= ttlS {
		return Fresh
	}
	return Stale
}

// PercentToBP accepts an exact plain decimal string (optional sign, no exponent
// or fraction syntax), multiplies percent by 100, and rounds halfway to even.
// It never passes through float64. Supported freeze-time output is 0..1000000.
func PercentToBP(percent string) (int64, error) {
	s := percent
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		s = s[1:]
	}
	digits, dots := 0, 0
	for _, r := range s {
		if r == '.' {
			dots++
		} else if r >= '0' && r <= '9' {
			digits++
		} else {
			return 0, fail("routing_invalid_percent", "percent must be a plain decimal string")
		}
	}
	if digits == 0 || dots > 1 {
		return 0, fail("routing_invalid_percent", "percent must be a plain decimal string")
	}
	n, ok := new(big.Rat).SetString(percent)
	if !ok || n.Sign() < 0 {
		return 0, fail("routing_invalid_percent", "percent must be nonnegative")
	}
	n.Mul(n, big.NewRat(100, 1))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n.Num(), n.Denom(), r)
	comparison := new(big.Int).Lsh(r, 1).Cmp(n.Denom())
	if comparison > 0 || (comparison == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Int64() > MaxUsedBP {
		return 0, fail("routing_invalid_percent", "rounded basis points outside supported range")
	}
	return q.Int64(), nil
}
