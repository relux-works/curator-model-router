// Package quota executes providerquota plans outside the pure router library.
package quota

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/relux-works/skill-agents-management/pkg/providerquota"
)

const maxOutput = 4 << 20

// Execute applies protocol completion to complete frames only. No vendor stdout
// or stderr is persisted or included in an error.
func Execute(parent context.Context, p providerquota.QuotaPlan, scratchRoot string) ([]byte, error) {
	if !p.Ready {
		return nil, providerquota.Refuse("plan_not_ready")
	}
	if p.CwdPolicy != providerquota.ScratchCwd || p.Timeout <= 0 || p.Timeout > 5*time.Minute || p.Binary == "" {
		return nil, providerquota.Refuse("plan_invalid")
	}
	cwd, err := os.MkdirTemp(scratchRoot, "cmr-quota-*")
	if err != nil {
		return nil, providerquota.Refuse("scratch_unavailable")
	}
	defer os.RemoveAll(cwd)
	ctx, cancel := context.WithTimeout(parent, p.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Binary, p.Argv...)
	cmd.Dir = cwd
	cmd.Env = p.Env
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, providerquota.Refuse("read_failure")
	}
	out := &protocolOutput{plan: p, stdin: stdin}
	cmd.Stdout = out
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return nil, providerquota.Refuse("read_failure")
	}
	if _, err = stdin.Write(p.Stdin); err != nil {
		cancel()
	}
	if !p.HoldStdinOpen {
		stdin.Close()
	}
	waitErr := cmd.Wait()
	stdin.Close()
	out.mu.Lock()
	defer out.mu.Unlock()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, providerquota.Refuse("timeout")
	}
	if out.err != nil {
		return nil, out.err
	}
	if waitErr != nil || err != nil {
		return nil, providerquota.Refuse("read_failure")
	}
	if p.HoldStdinOpen && !out.complete {
		return nil, providerquota.Refuse("response_missing")
	}
	return append([]byte(nil), out.b.Bytes()...), nil
}

type protocolOutput struct {
	mu                    sync.Mutex
	b                     bytes.Buffer
	plan                  providerquota.QuotaPlan
	stdin                 io.WriteCloser
	initialized, complete bool
	err                   error
}

func (o *protocolOutput) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.b.Len()+len(b) > maxOutput {
		o.err = providerquota.Refuse("output_too_large")
		o.stdin.Close()
		return 0, o.err
	}
	o.b.Write(b)
	if !o.plan.HoldStdinOpen {
		return len(b), nil
	}
	if !o.initialized && len(o.plan.AfterInitialize) > 0 && providerquota.RPCComplete(1)(o.b.Bytes()) {
		if _, err := providerquota.RPCResult(o.b.Bytes(), 1); err != nil {
			o.err = err
			o.stdin.Close()
			return len(b), nil
		}
		o.initialized = true
		if _, err := o.stdin.Write(o.plan.AfterInitialize); err != nil {
			o.err = providerquota.Refuse("read_failure")
			return len(b), o.err
		}
	}
	if !o.complete && o.plan.Complete != nil && o.plan.Complete(o.b.Bytes()) {
		o.complete = true
		o.stdin.Close()
	}
	return len(b), nil
}
