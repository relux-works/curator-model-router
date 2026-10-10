package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Run the wrapper in a separate process so os.Exit cannot hide incomplete
// advisory cleanup. The only task-board available is a temporary fake.
func TestShadowProcessHelper(t *testing.T) {
	if os.Getenv("CMR_PROCESS_HELPER") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("CMR_PROCESS_ARGS")), &args); err != nil {
		os.Exit(99)
	}
	os.Exit(run(args, os.Stdout, os.Stderr))
}

func shadowProcess(t *testing.T, args []string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestShadowProcessHelper$")
	cmd.Env = append(os.Environ(), "CMR_PROCESS_HELPER=1", "CMR_PROCESS_ARGS="+string(encoded))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	output := new(bytes.Buffer)
	cmd.Stdout, cmd.Stderr = output, output
	return cmd, output
}

func waitShadowMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for fake marker %s", filepath.Base(path))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertShadowExit(t *testing.T, err error, want int, output *bytes.Buffer) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != want {
		t.Fatalf("want exit %d, got %v; %s", want, err, output.String())
	}
	if observation := readShadowObservations(t)[0]; observation.TaskBoardExitCode != want {
		t.Fatalf("lost child status in observation: %+v", observation)
	}
}

func TestShadowProcessReapsAdvisoryDescendants(t *testing.T) {
	for _, ignoreTerm := range []bool{false, true} {
		t.Run(strconv.FormatBool(ignoreTerm), func(t *testing.T) {
			root := cliEnvironment(t)
			t.Setenv("LIVE_ROOT", root)
			policy := filepath.Join(root, "policy.toml")
			if err := os.WriteFile(policy, []byte("budget_mode = 'economy'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			// The fake advisory owns a genuine descendant. Its parent reaps that
			// descendant on TERM, including when it otherwise ignores termination.
			trap := `trap 'wait "$descendant"; exit 0' TERM`
			if ignoreTerm {
				trap = `trap 'wait "$descendant"; while :; do :; done' TERM`
			}
			script := `#!/bin/sh
if [ "$1" = spawn ]; then
 while [ ! -e "$LIVE_ROOT/advisory-ready" ]; do /bin/sleep 0.01; done
 exit 23
fi
/bin/sleep 30 &
descendant=$!
` + trap + `
printf '%s\n%s\n' "$$" "$descendant" > "$LIVE_ROOT/advisory-pids"
: > "$LIVE_ROOT/advisory-ready"
wait "$descendant"
`
			if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if !t.Failed() {
					return
				}
				raw, _ := os.ReadFile(filepath.Join(root, "advisory-pids"))
				for _, value := range strings.Fields(string(raw)) {
					if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
			cmd, output := shadowProcess(t, []string{"spawn", "--mode", "shadow", "--policy", policy, "--task-class", "code", "--", "TASK", "--role=developer"})
			started := time.Now()
			assertShadowExit(t, cmd.Run(), 23, output)
			if time.Since(started) > 3*time.Second {
				t.Fatal("advisory cleanup exceeded its bound")
			}
			raw, err := os.ReadFile(filepath.Join(root, "advisory-pids"))
			if err != nil {
				t.Fatal(err)
			}
			pids := strings.Fields(string(raw))
			if len(pids) != 2 {
				t.Fatal("missing advisory parent/descendant PIDs", string(raw))
			}
			for _, value := range pids {
				pid, err := strconv.Atoi(value)
				if err != nil || pid <= 0 {
					t.Fatal("invalid fake PID", value)
				}
				if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Fatalf("advisory process %d survived cmr exit: %v", pid, err)
				}
			}
			o := readShadowObservations(t)[0]
			if o.CMRError == nil || o.CMRError.Code != "advisory_timeout" || o.Budget != "economy" {
				t.Fatalf("incorrect timeout observation: %+v", o)
			}
		})
	}
}

func TestShadowSignalExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
	}{{"TERM", syscall.SIGTERM}, {"INT", syscall.SIGINT}, {"PIPE", syscall.SIGPIPE}} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliEnvironment(t)
			script := "#!/bin/sh\nkill -s " + tc.name + " $$\nexit 23\n"
			if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			cmd, output := shadowProcess(t, []string{"spawn", "--mode", "shadow", "--policy", "/nonexistent/policy.toml", "--", "TASK"})
			assertShadowExit(t, cmd.Run(), 128+int(tc.sig), output)
		})
	}
}

func TestShadowForwardsParentOnlySignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			root := cliEnvironment(t)
			t.Setenv("LIVE_ROOT", root)
			script := "#!/bin/sh\n: > \"$LIVE_ROOT/child-ready\"\nexec /bin/sleep 30\n"
			if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			cmd, output := shadowProcess(t, []string{"spawn", "--mode", "shadow", "--policy", "/nonexistent/policy.toml", "--", "TASK"})
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			waitShadowMarker(t, filepath.Join(root, "child-ready"))
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			assertShadowExit(t, cmd.Wait(), 128+int(sig), output)
		})
	}
}
