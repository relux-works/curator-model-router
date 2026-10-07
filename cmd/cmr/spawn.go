package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

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
	policy, policyErr := cmrio.LoadPolicy(o.policy)
	mode := o.mode
	if mode == "" {
		mode = string(policy.Mode)
	}
	if mode == "shadow" {
		// A write to a closed stdout/stderr pipe would otherwise kill cmr with
		// SIGPIPE before the caller's launch; shadow must stay fail-open.
		// Catching (not ignoring) keeps the default disposition for task-board:
		// caught signals reset to default on exec, ignored ones would not.
		signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
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
	// Caller flags are locks, including flags after positional spawn arguments.
	for _, x := range []struct {
		name   string
		target *string
	}{{"agent", &o.agent}, {"model", &o.model}, {"reasoning-effort", &o.effort}, {"role", &o.role}} {
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
	var advisory bytes.Buffer
	if policyErr == nil {
		o.loadedPolicy = &policy
		record, _, recErr := spawnRecommendation(o, forwarded)
		if recErr == nil {
			// recommendation already persisted the full replayable decision,
			// including refusals. Also expose the would-be result on stderr.
			_ = outputRecommendation(record, true, nil, "shadow", &advisory)
		} else {
			commandError(recErr, true, &advisory, io.Discard)
		}
	} else {
		commandError(policyErr, true, &advisory, io.Discard)
	}
	// Even an unavailable decision log or stderr must not gate shadow launch.
	_, _ = fmt.Fprintf(stderr, "cmr:shadow %s\n", strings.TrimSpace(advisory.String()))
	commands := [][]string{append([]string{"task-board", "spawn"}, forwarded...)}
	return executeSpawnCommandsWithStatus(binary, commands, o.json, stdout, stderr, true)
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
