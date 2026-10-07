package main

import (
	"flag"
	"strings"
)

// optionBoundary finds an unconsumed terminator using the registered arities.
// String values such as "--" or "--json" are always consumed literally.
func optionBoundary(f *flag.FlagSet, args []string) (int, error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return i, nil
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			return -1, &Refusal{"cmr_invalid_arguments", "unexpected positional argument"}
		}
		name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		x := f.Lookup(name)
		if x == nil {
			return -1, &Refusal{"cmr_invalid_arguments", "unknown flag: " + name}
		}
		boolean := false
		if b, ok := x.Value.(interface{ IsBoolFlag() bool }); ok {
			boolean = b.IsBoolFlag()
		}
		if !boolean && !inline {
			i++
			if i == len(args) {
				return -1, &Refusal{"cmr_invalid_arguments", "missing flag value"}
			}
		}
	}
	return -1, nil
}
