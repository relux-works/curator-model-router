package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func runSpawn(args []string, stdout, stderr io.Writer) int {
	var wrapper recommendOptions
	f := recommendFlagSet("spawn", true, &wrapper)
	boundary, scanErr := optionBoundary(f, args)
	if scanErr != nil {
		return commandError(scanErr, hasJSON(args), stdout, stderr)
	}
	if boundary < 0 {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "spawn requires -- before task-board arguments"}, hasJSON(args), stdout, stderr)
	}
	o, err := recommendFlags("spawn", args[:boundary], true)
	if err != nil {
		return commandError(err, hasJSON(args[:boundary]), stdout, stderr)
	}
	if o.mode != "" && o.mode != "select" && o.mode != "recommend" && o.mode != "shadow" {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "mode must be select, recommend or shadow"}, o.json, stdout, stderr)
	}
	forwarded := args[boundary+1:]
	// Resolve execution mode before any advisory admission, task, or usage work.
	// Freeze the policy once so recommendation cannot observe a different mode.
	// An explicit shadow flag needs no policy IO before the child's Start.
	var policy recommend.Policy
	var policyErr error
	mode := o.mode
	if mode == "" {
		hint, load := cmrio.PreparePolicy(o.policy)
		mode = string(hint)
		if mode == "shadow" {
			o.policyLoader = load
			policy.Mode = hint
		} else {
			policy, policyErr = load()
		}
	} else if mode != "shadow" {
		policy, policyErr = cmrio.LoadPolicy(o.policy)
	}

	if mode == "shadow" {
		// A write to a closed stdout/stderr pipe would otherwise kill cmr with
		// SIGPIPE before the caller's launch; shadow must stay fail-open.
		// Catching (not ignoring) keeps the default disposition for task-board:
		// caught signals reset to default on exec, ignored ones would not.
		sigpipe := make(chan os.Signal, 1)
		signal.Notify(sigpipe, syscall.SIGPIPE)
		defer signal.Stop(sigpipe)
		return runShadowSpawn(o, forwarded, policy, policyErr, stdout, stderr)
	}
	if policyErr != nil {
		return commandError(policyErr, o.json, stdout, stderr)
	}
	if mode != "select" && mode != "recommend" && mode != "shadow" {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "mode must be select, recommend or shadow"}, o.json, stdout, stderr)
	}
	o.loadedPolicy = &policy
	record, parsed, err := spawnRecommendation(o, forwarded)
	if err != nil {
		return commandError(err, o.json, stdout, stderr)
	}
	if record.Recommendation.Refusal != nil {
		if err = outputRecommendation(record, o.json, nil, mode, stdout); err != nil {
			return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write recommendation"}, o.json, stdout, stderr)
		}
		return 2
	}
	commands := spawnCommands(record, forwarded, parsed, mode)
	if mode == "recommend" {
		if err = outputRecommendation(record, o.json, commands, mode, stdout); err != nil {
			return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write recommendation"}, o.json, stdout, stderr)
		}
		return 0
	}
	if _, err = fmt.Fprintf(stderr, "cmr:%s %s admission=%s\n", record.DecisionID, mode, record.Inputs.AdmissionSource); err != nil {
		return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not log decision"}, o.json, stdout, stderr)
	}
	binary, err := exec.LookPath("task-board")
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_task_board_unavailable", Message: "task-board is not on PATH"}, o.json, stdout, stderr)
	}
	return executeSpawnCommands(binary, commands, o.json, stdout, stderr)
}

func spawnRecommendation(o recommendOptions, forwarded []string) (recommend.DecisionRecord, spawnArguments, error) {
	parsed, err := parseSpawnArgs(forwarded)
	if err != nil {
		return recommend.DecisionRecord{}, parsed, err
	}
	// Forwarded role always defines admission. Selection flags are locks in
	// select/recommend, including flags after positional spawn arguments.
	for _, x := range []struct {
		name   string
		target *string
	}{{"agent", &o.agent}, {"model", &o.model}, {"reasoning-effort", &o.effort}, {"role", &o.role}} {
		if o.mode == "shadow" && x.name != "role" {
			continue // Only cmr-side selection flags constrain the advisory pick.
		}
		if v, ok := parsed.locks[x.name]; ok {
			if *x.target != "" && *x.target != v && !(x.name == "model" && canonicalModel(*x.target) == canonicalModel(v)) {
				return recommend.DecisionRecord{}, parsed, &Refusal{Code: "cmr_invalid_arguments", Message: "conflicting --" + x.name}
			}
			*x.target = v
		}
	}
	o.admission = &recommend.AdmissionContext{BoardFlags: parsed.boardFlags}
	record, err := recommendation(o)
	return record, parsed, err
}

func runShadowSpawn(o recommendOptions, forwarded []string, policy recommend.Policy, policyErr error, stdout, stderr io.Writer) int {
	binary, err := exec.LookPath("task-board")
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_task_board_unavailable", Message: "task-board is not on PATH"}, o.json, stdout, stderr)
	}
	// Start the real spawn with exactly the forwarded argv before parsing board
	// arguments, reading advisory files, or querying preflight.
	cmd := exec.Command(binary, append([]string{"spawn"}, forwarded...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Start(); err != nil {
		return writeRefusal(&Refusal{Code: "cmr_spawn_failed", Message: "could not execute task-board"}, o.json, stdout, stderr)
	}
	timeout := o.advisoryTimeout
	if timeout == 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	o.ctx, o.mode = ctx, "shadow"
	type result struct {
		record recommend.DecisionRecord
		parsed spawnArguments
		policy recommend.Policy
		err    error
	}
	done := make(chan result, 1)
	go func(o recommendOptions) {
		r := result{policy: policy, err: policyErr}
		if ctx.Err() != nil {
			r.err = &Refusal{Code: "advisory_timeout", Message: "advisory recommendation timed out"}
			done <- r
			return
		}
		// An explicit shadow mode defers policy loading until after child startup.
		if o.policyLoader != nil {
			r.policy, r.err = o.policyLoader()
		} else if policy.SchemaVersion == "" && policyErr == nil {
			r.policy, r.err = cmrio.LoadPolicy(o.policy)
		}
		if r.err == nil {
			o.loadedPolicy = &r.policy
			r.record, r.parsed, r.err = spawnRecommendation(o, forwarded)
		} else {
			r.parsed, _ = parseSpawnArgs(forwarded)
		}
		if ctx.Err() != nil && r.err == nil {
			r.err = &Refusal{Code: "advisory_timeout", Message: "advisory recommendation timed out"}
		}
		done <- r
	}(o)
	_ = cmd.Wait()
	// Completed child status is authoritative, including output relay failures.
	if cmd.ProcessState == nil {
		return writeRefusal(&Refusal{Code: "cmr_spawn_failed", Message: "could not wait for task-board"}, o.json, stdout, stderr)
	}
	status := cmd.ProcessState.ExitCode()
	var r result
	select {
	case r = <-done:
	default:
		// Do not wait for an advisory that outlives the launch. Its own context also
		// bounds work while a long-running child remains active.
		r.policy = policy
		r.parsed, _ = parseSpawnArgs(forwarded)
		r.err = &Refusal{Code: "advisory_timeout", Message: "advisory recommendation timed out"}
	}
	cancel()
	var advisory bytes.Buffer
	if r.err == nil {
		_ = outputRecommendation(r.record, true, nil, "shadow", &advisory)
	} else {
		commandError(r.err, true, &advisory, io.Discard)
	}
	// Write only after Wait so advisory and child never concurrently use writers.
	_, _ = fmt.Fprintf(stderr, "cmr:shadow %s\n", strings.TrimSpace(advisory.String()))
	observation := newShadowObservation(o, r.parsed, r.policy, r.record, r.err, status, time.Now().UTC())
	if err := appendShadowObservation(cmrio.StateRoot(), observation); err != nil {
		_, _ = fmt.Fprintln(stderr, "cmr:shadow warning: could not append observation")
	}

	return status
}

func executeSpawnCommands(binary string, commands [][]string, asJSON bool, stdout, stderr io.Writer) int {
	return executeSpawnCommandsWithStatus(binary, commands, asJSON, stdout, stderr, false)
}

func executeSpawnCommandsWithStatus(binary string, commands [][]string, asJSON bool, stdout, stderr io.Writer, preserveStatus bool) int {
	// Run sequentially so fan-out has one real spawn gate per configuration and
	// its status/output reaches the caller. The stored decision precedes launch.
	for _, command := range commands {
		cmd := exec.Command(binary, command[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			// Wait can return a relay error after a successful child. In shadow,
			// completed child status is authoritative; a start failure has no state.
			if preserveStatus && cmd.ProcessState != nil {
				return cmd.ProcessState.ExitCode()
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return exit.ExitCode()
			}
			return writeRefusal(&Refusal{Code: "cmr_spawn_failed", Message: "could not execute task-board"}, asJSON, stdout, stderr)
		}
	}
	return 0
}
func spawnCommands(record recommend.DecisionRecord, args []string, parsed spawnArguments, mode string) [][]string {
	if mode == "shadow" {
		return [][]string{append([]string{"task-board", "spawn"}, args...)}
	}
	selected := []recommend.Candidate{*record.Recommendation.Selected}
	if record.Inputs.Task.Pipeline == "fanout" {
		selected = record.Recommendation.FanOut
	}
	commands := [][]string{}
	for _, c := range selected {
		added := []string{}
		// The launch must use the role whose admission and rules we evaluated.
		if _, ok := parsed.locks["role"]; !ok {
			added = append(added, "--role", record.Inputs.Task.Role)
		}
		// Fan-out launches several runs of one element; task-board returns the
		// existing live run unless parallel runs are requested explicitly.
		if len(selected) > 1 && !parsed.allowParallel {
			added = append(added, "--allow-parallel")
		}
		flags := spawnFlags(c)
		for i := 0; i < len(flags); i += 2 {
			if _, ok := parsed.locks[strings.TrimPrefix(flags[i], "--")]; !ok {
				added = append(added, flags[i], flags[i+1])
			}
		}
		if record.Inputs.Admission != nil && record.Inputs.Admission.RationaleRequired[c.Runtime] && !parsed.rationalePresent {
			added = append(added, "--selection-rationale", "cmr:"+record.DecisionID+" task="+record.Inputs.Task.TaskClass+" admission="+record.Inputs.AdmissionSource)
		}
		// Inject before task-board's own -- terminator, preserving all caller bytes.
		boundary := parsed.injectionIndex
		command := append([]string{"task-board", "spawn"}, args[:boundary]...)
		command = append(command, added...)
		command = append(command, args[boundary:]...)
		commands = append(commands, command)
	}
	return commands
}
