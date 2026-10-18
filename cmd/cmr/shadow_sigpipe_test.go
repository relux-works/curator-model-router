package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/relux-works/curator-model-router/internal/cmrio"
)

// Shadow must launch even when cmr's own stderr is a pipe without a reader:
// the advisory write would otherwise end cmr with SIGPIPE before task-board.
func TestShadowLaunchesWithClosedStderrPipe(t *testing.T) {
	if os.Getenv("CMR_SIGPIPE_CHILD") == "1" {
		cmrio.SystemConfigRoot = filepath.Join(os.Getenv("HOME"), "system")
		os.Exit(run([]string{"spawn", "--task-class", "code.implement", "--mode", "shadow", "--policy", filepath.Join(os.Getenv("HOME"), "missing-policy.toml"), "--", "TASK", "--role", "developer", "--background"}, os.Stdout, os.Stderr))
	}
	root := cliEnvironment(t)
	log := filepath.Join(root, "spawn-argv")
	script := "#!/bin/sh\n/usr/bin/printf '%s\\n' \"$@\" >> \"" + log + "\"\nexit 23\n"
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestShadowLaunchesWithClosedStderrPipe$")
	cmd.Env = append(os.Environ(), "CMR_SIGPIPE_CHILD=1", "PATH="+root+":/usr/bin:/bin", "HOME="+root, "XDG_STATE_HOME="+root, "XDG_CONFIG_HOME="+root)
	cmd.Stderr = w
	err = cmd.Run()
	w.Close()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("want task-board's exit 23, got %v", err)
	}
	if b, _ := os.ReadFile(log); len(b) == 0 {
		t.Fatal("task-board was not launched")
	}
}

// The launched task-board must keep the default SIGPIPE disposition.
func TestShadowChildKeepsDefaultSigpipe(t *testing.T) {
	if os.Getenv("CMR_SIGPIPE_CHILD") == "2" {
		cmrio.SystemConfigRoot = filepath.Join(os.Getenv("HOME"), "system")
		os.Exit(run([]string{"spawn", "--task-class", "code.implement", "--mode", "shadow", "--policy", filepath.Join(os.Getenv("HOME"), "missing-policy.toml"), "--", "TASK", "--role", "developer", "--background"}, os.Stdout, os.Stderr))
	}
	root := cliEnvironment(t)
	script := "#!/bin/sh\nkill -s PIPE $$\nexit 23\n"
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestShadowChildKeepsDefaultSigpipe$")
	cmd.Env = append(os.Environ(), "CMR_SIGPIPE_CHILD=2", "PATH="+root+":/usr/bin:/bin", "HOME="+root, "XDG_STATE_HOME="+root, "XDG_CONFIG_HOME="+root)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 141 {
		t.Fatalf("want task-board's SIGPIPE status 141, got %v", err)
	}
	t.Setenv("XDG_STATE_HOME", root)
	if observation := readShadowObservations(t)[0]; observation.TaskBoardExitCode != 141 {
		t.Fatalf("lost SIGPIPE status in observation: %+v", observation)
	}
}
