package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/internal/subprocess"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

// shadowFinishCommand builds the detached advisory process. Tests replace it
// because the test binary is not cmr.
var shadowFinishCommand = func(args []string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(self, args...), nil
}

// deferredAdvisoryBudget is the least time a detached advisory gets. It
// recomputes the advice from scratch, and on a loaded board the two sequential
// spawn-preflight queries (role, then agent) alone take 20-30 s each, so the
// launch deadline's remainder (about 59 s after a background spawn) is not
// enough. The detached process blocks nothing, and the watchdog still bounds it.
const deferredAdvisoryBudget = 3 * time.Minute

// deferShadowAdvice starts `cmr shadow-finish` in its own session with no
// inherited stdio, so neither the caller's pipes nor its process group keep it
// alive or kill it. It returns the PID, or 0 when the hand-off failed and the
// caller must record the timeout itself.
func deferShadowAdvice(wrapper, forwarded []string, status int, remaining time.Duration) int {
	remaining = remaining.Truncate(time.Millisecond)
	if remaining <= 0 {
		return 0
	}
	remaining = max(remaining, deferredAdvisoryBudget)
	args := []string{"shadow-finish", "--exit-status", strconv.Itoa(status)}
	args = append(args, wrapper...)
	// The last occurrence wins, so the deferred budget overrides the caller's.
	args = append(args, "--advisory-timeout", remaining.String(), "--")
	args = append(args, forwarded...)
	cmd, err := shadowFinishCommand(args)
	if err != nil {
		return 0
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid
}

// runShadowFinish computes a deferred shadow advisory and appends its
// observation. It never launches task-board: the launch already happened, and
// its completed exit status arrives as --exit-status.
func runShadowFinish(args []string, _, _ io.Writer) int {
	if len(args) < 2 || args[0] != "--exit-status" {
		return 2
	}
	status, err := strconv.Atoi(args[1])
	if err != nil {
		return 2
	}
	args = args[2:]
	var scan recommendOptions
	boundary, err := optionBoundary(recommendFlagSet("spawn", true, &scan), args)
	if err != nil || boundary < 0 {
		return 2
	}
	o, err := recommendFlags("spawn", args[:boundary], true)
	if err != nil {
		return 2
	}
	forwarded := args[boundary+1:]
	// Hard lifetime bound: context cancellation cannot interrupt blocking file IO
	// (for example a policy path that is a FIFO without a writer).
	watchdog := time.AfterFunc(o.advisoryTimeout+5*time.Second, func() { os.Exit(124) })
	defer watchdog.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), o.advisoryTimeout)
	defer cancel()
	ctx, processes := subprocess.WithScope(ctx)
	o.ctx, o.mode = ctx, "shadow"
	type result struct {
		record recommend.DecisionRecord
		parsed spawnArguments
		policy recommend.Policy
		source string
		err    error
	}
	done := make(chan result, 1)
	resolvedPolicy := make(chan result, 1)
	go func(o recommendOptions) {
		var r result
		var source cmrio.ConfigSource
		r.policy, source, r.err = cmrio.LoadPolicyWithSource(o.policy)
		r.source = source.Layer
		if r.err == nil {
			// Publish immutable metadata before any slow preflight or quota work.
			resolvedPolicy <- result{policy: r.policy, source: r.source}
			o.loadedPolicy = &r.policy
			r.record, r.parsed, r.err = spawnRecommendation(o, forwarded)
		} else {
			r.parsed, _ = parseSpawnArgs(forwarded)
		}
		done <- r
	}(o)
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		select {
		case r = <-done:
		default:
			r.parsed, _ = parseSpawnArgs(forwarded)
			r.err = &Refusal{Code: "advisory_timeout", Message: "advisory recommendation timed out"}
			select {
			case metadata := <-resolvedPolicy:
				r.policy, r.source = metadata.policy, metadata.source
			default:
			}
		}
	}
	// Whichever receive won, a result completed after the deadline is a timeout.
	if ctx.Err() != nil && r.err == nil {
		r.err = &Refusal{Code: "advisory_timeout", Message: "advisory recommendation timed out"}
	}
	cancel()
	cleanup, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	_ = processes.Close(cleanup)
	if r.source == "" {
		source, _ := cmrio.ResolveConfig("policy", o.policy)
		r.source = source.Layer
	}
	o.policySource = r.source
	policy, record, parsed, recErr := r.policy, r.record, r.parsed, r.err
	observation := newShadowObservation(o, parsed, policy, record, recErr, status, time.Now().UTC())
	if err := appendShadowObservation(cmrio.StateRoot(), observation); err != nil {
		return 1
	}
	return 0
}
