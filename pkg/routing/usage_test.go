package routing

import (
	"fmt"
	"testing"
)

func ptr[T any](v T) *T { return &v }
func TestFreshness(t *testing.T) {
	type testCase struct {
		name      string
		window    UsageWindow
		asOf, ttl int64
		want      Freshness
	}
	const now int64 = 1800000000
	base := UsageWindow{ID: "session", Scope: "all", Minutes: 300, UsedBP: ptr(int64(0)), ObservedAt: ptr(now), ResetsAt: ptr(now + 600)}
	tests := []testCase{
		{"observed equals as_of", base, now, 600, Fresh},
		{"missing used_bp", func() UsageWindow { w := base; w.UsedBP = nil; return w }(), now, 600, Invalid},
		{"missing observed", func() UsageWindow { w := base; w.ObservedAt = nil; return w }(), now, 600, Invalid},
		{"future observed", func() UsageWindow { w := base; w.ObservedAt = ptr(now + 1); return w }(), now, 600, Invalid},
		{"no reset", func() UsageWindow { w := base; w.ResetsAt = nil; return w }(), now, 600, Fresh},
		{"invalid precedes expired", func() UsageWindow { w := base; w.ObservedAt = nil; w.ResetsAt = ptr(now); return w }(), now, 600, Invalid},
	}
	for _, delta := range []int64{-1, 0, 1} {
		w := base
		w.ObservedAt = ptr(now - 600 - delta)
		want := Fresh
		if delta > 0 {
			want = Stale
		}
		tests = append(tests, testCase{fmt.Sprintf("age ttl %+d", delta), w, now, 600, want})
		w = base
		w.ResetsAt = ptr(now + delta)
		want = Fresh
		if delta <= 0 {
			want = Expired
		}
		tests = append(tests, testCase{fmt.Sprintf("reset as_of %+d", delta), w, now, 600, want})
	}
	for _, bounds := range []struct {
		field    string
		min, max int64
	}{{"minutes", MinMinutes, MaxMinutes}, {"used_bp", MinUsedBP, MaxUsedBP}, {"ttl", MinTTL, MaxTTL}, {"as_of", MinTimestamp, MaxTimestamp}, {"observed_at", MinTimestamp, MaxTimestamp}, {"resets_at", MinTimestamp, MaxTimestamp}} {
		for _, bound := range []int64{bounds.min, bounds.max} {
			for _, delta := range []int64{-1, 0, 1} {
				value := bound + delta
				w := base
				at, ttl := now, int64(600)
				want := Fresh
				switch bounds.field {
				case "minutes":
					w.Minutes = value
				case "used_bp":
					w.UsedBP = ptr(value)
				case "ttl":
					ttl = value
				case "as_of":
					at = value
					w.ObservedAt = ptr(value)
					w.ResetsAt = nil
				case "observed_at":
					at = MaxTimestamp
					w.ObservedAt = ptr(value)
					w.ResetsAt = nil
					if value >= MinTimestamp && value <= MaxTimestamp && at-value > ttl {
						want = Stale
					}
				case "resets_at":
					w.ResetsAt = ptr(value)
					if value <= at {
						want = Expired
					}
				}
				if value < bounds.min || value > bounds.max {
					want = Invalid
				}
				tests = append(tests, testCase{fmt.Sprintf("%s %d %+d", bounds.field, bound, delta), w, at, ttl, want})
			}
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyFreshness(tc.window, tc.asOf, tc.ttl)
			if got != tc.want {
				t.Fatalf("window=%+v as_of=%d ttl=%d got=%s want=%s", tc.window, tc.asOf, tc.ttl, got, tc.want)
			}
		})
	}
}
func TestPercentToBP(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int64
		bad   bool
	}{
		{"0", 0, false}, {"0.005", 0, false}, {"0.015", 2, false}, {"0.025", 2, false}, {"0.035", 4, false},
		{"28.125", 2812, false}, {"28.135", 2814, false}, {"100", 10000, false}, {"101.25", 10125, false}, {"10000", 1000000, false},
		{"+1.005", 100, false}, {"1.0050000000000001", 101, false}, {"1.0049999999999999", 100, false},
		{"-1", 0, true}, {"NaN", 0, true}, {"Inf", 0, true}, {"1e2", 0, true}, {"1/2", 0, true}, {"", 0, true}, {"1.2.3", 0, true}, {"10000.01", 0, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := PercentToBP(tc.input)
			if (err != nil) != tc.bad || (!tc.bad && got != tc.want) {
				t.Fatalf("got=%d err=%v want=%d bad=%v", got, err, tc.want, tc.bad)
			}
		})
	}
}
