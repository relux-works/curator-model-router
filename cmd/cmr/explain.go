package main

import (
	"github.com/relux-works/curator-model-router/pkg/routing"
	"io"
)

func runExplain(args []string, out, stderr io.Writer) int {
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
	ex, err := routing.ExplainDecision(b, d)
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	return routeOutput(ex, ex.RenderHuman(), args, out, stderr)
}
