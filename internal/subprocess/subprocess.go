// Package subprocess owns advisory process groups and their cleanup.
package subprocess

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type scopeKey struct{}

// Scope prevents new subprocesses during shutdown and tracks their reaping.
type Scope struct {
	mu       sync.Mutex
	closed   bool
	children map[*Child]struct{}
}

func WithScope(ctx context.Context) (context.Context, *Scope) {
	s := &Scope{children: make(map[*Child]struct{})}
	return context.WithValue(ctx, scopeKey{}, s), s
}

type Child struct {
	cmd    *exec.Cmd
	scope  *Scope
	once   sync.Once
	exited chan struct{}
	done   chan struct{}
}

// Start isolates a CommandContext advisory from the caller's launch. Every
// successful Start must be paired with Wait, including protocol/output failures.
func Start(ctx context.Context, cmd *exec.Cmd) (*Child, error) {
	s, _ := ctx.Value(scopeKey{}).(*Scope)
	if s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return nil, context.Canceled
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := &Child{cmd: cmd, scope: s, exited: make(chan struct{}), done: make(chan struct{})}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { c.terminate(); return nil }
	cmd.WaitDelay = 200 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if s != nil {
		s.children[c] = struct{}{}
	}
	return c, nil
}

func (c *Child) terminate() {
	c.once.Do(func() {
		// Give cooperative parents time to reap their children, then kill the
		// entire group even if its leader has already exited.
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-c.exited:
		case <-timer.C:
		}
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	})
}

func (c *Child) Wait() error {
	err := c.cmd.Wait()
	close(c.exited)
	c.terminate()
	if c.scope != nil {
		c.scope.mu.Lock()
		delete(c.scope.children, c)
		c.scope.mu.Unlock()
	}
	close(c.done)
	return err
}

// Close synchronizes group termination and reaping within the caller's bound.
// Closing the scope also prevents late advisory work from starting new groups.
func (s *Scope) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	children := make([]*Child, 0, len(s.children))
	for c := range s.children {
		children = append(children, c)
	}
	s.mu.Unlock()
	for _, c := range children {
		go c.terminate()
	}
	for _, c := range children {
		select {
		case <-c.done:
		case <-ctx.Done():
			// An unresponsive waiter must still leave no running group.
			for _, c := range children {
				select {
				case <-c.done:
				default:
					_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
				}
			}
			return ctx.Err()
		}
	}
	return nil
}

// ExitStatus uses the shell convention for signal termination.
func ExitStatus(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}
