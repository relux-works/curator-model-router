package main

import (
	"github.com/relux-works/curator-model-router/pkg/routing"
	"io"
)

func runHeadroom(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "explain" {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "usage: cmr headroom explain --snapshot FILE [--policy FILE] [--role R] [--json]"}, hasJSON(args), out, stderr)
	}
	f, err := routeFlags(args[1:], "--snapshot", "--policy", "--role")
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	raw, err := readInput(f, "--snapshot")
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	s, err := routing.LoadSnapshot(raw)
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	p := routing.DefaultPolicy(s.SchemaVersion)
	p.Headroom.Enabled = true
	p.Mode = routing.ModeRecommend
	if _, ok := f["--policy"]; ok {
		raw, err = readInput(f, "--policy")
		if err == nil {
			p, err = routing.LoadPolicy(raw)
		}
		if err != nil {
			return routeError(err, args, out, stderr)
		}
	}
	role := f["--role"]
	if role == "" {
		role = routing.Unknown
	}
	ex, err := routing.Explain(routing.DecisionBundle{Snapshot: s, Policy: p, Envelope: routing.TaskEnvelope{Role: role}, Versions: routing.DecisionOptions{SelectorVersion: routing.SelectorVersion}})
	if err != nil {
		return routeError(err, args, out, stderr)
	}
	return routeOutput(ex, ex.RenderHuman(), args, out, stderr)
}
