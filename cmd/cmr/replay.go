package main

import (
	"fmt"
	"github.com/relux-works/curator-model-router/pkg/routing"
	"io"
	"strings"
)

func runReplay(args []string, out, stderr io.Writer) int {
	f, err := routeFlags(args, "--decision", "--input")
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	b, err := bundleInput(f)
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	d, err := decisionInput(f)
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	r, err := routing.Replay(b, &d)
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	var human strings.Builder
	fmt.Fprintf(&human, "equal=%t\n", r.Equal)
	for _, diff := range r.Differences {
		fmt.Fprintf(&human, "%s: stored=%s recomputed=%s\n", diff.Member, diff.Stored, diff.Recomputed)
	}
	code := routeOutput(r, human.String(), args, out, stderr)
	if code == 0 && !r.Equal {
		return 1
	}
	return code
}
