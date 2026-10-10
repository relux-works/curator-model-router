package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestShadowSlowAdvisoryNeverDelaysLaunch(t *testing.T) {
	root := cliEnvironment(t)
	policy := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(policy, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVE_ROOT", root)
	script := `#!/bin/sh
if [ "$1" = spawn ]; then
 printf '%s\000' "$@" > "$LIVE_ROOT/launched"
 # Hold the child until the test has seen the launch while preflight is blocked.
 while [ ! -e "$LIVE_ROOT/release" ]; do /bin/sleep 0.01; done
 exit 23
fi
: > "$LIVE_ROOT/query-started"
exec /bin/sleep 5
`
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	forwarded := []string{"TASK with spaces", "--role=developer", "--background", "--", "literal", ""}
	args := []string{"spawn", "--mode", "shadow", "--policy", policy, "--task-class", "implementation", "--"}
	args = append(args, forwarded...)
	var out, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(args, &out, &stderr) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, launchErr := os.Stat(filepath.Join(root, "launched"))
		_, queryErr := os.Stat(filepath.Join(root, "query-started"))
		if launchErr == nil && queryErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("launch waited for the slow preflight")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 23 {
			t.Fatal(code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper waited for unfinished advisory")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "launched"))
	if string(raw) != strings.Join(append([]string{"spawn"}, forwarded...), "\x00")+"\x00" {
		t.Fatal("argv changed", raw)
	}
	observation := readShadowObservations(t)[0]
	if observation.CMRError == nil || observation.CMRError.Code != "advisory_timeout" || observation.TaskBoardExitCode != 23 || observation.TaskClass != "code.implement" || observation.OriginalTaskClass != "implementation" {
		t.Fatalf("%+v", observation)
	}
}

func TestShadowAdvisoryDeadlineWhileChildContinues(t *testing.T) {
	root := cliEnvironment(t)
	policy := filepath.Join(root, "policy.toml")
	os.WriteFile(policy, nil, 0600)
	script := `#!/bin/sh
if [ "$1" = spawn ]; then /bin/sleep 1; exit 17; fi
exec /bin/sleep 5
`
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"spawn", "--mode", "shadow", "--advisory-timeout", "100ms", "--preflight-timeout", "5s", "--policy", policy, "--task-class", "code", "--", "TASK", "--role=developer"}, &out, &stderr); code != 17 {
		t.Fatal(code)
	}
	observation := readShadowObservations(t)[0]
	if observation.CMRError == nil || observation.CMRError.Code != "preflight_timeout" {
		t.Fatalf("%+v", observation)
	}
}

func TestShadowSpecificSanitizedErrorsAndFailOpenGroups(t *testing.T) {
	cliEnvironment(t)
	at := time.Now()
	for _, code := range []string{"preflight_timeout", "preflight_failed", "preflight_timeout", "invalid_task"} {
		original := "code"
		err := error(&recommend.Refusal{Code: code, Message: "private-path provider-output"})
		if code == "invalid_task" {
			original = "x\nsecret"
			err = &recommend.Refusal{Code: code, Message: `unknown class "x\nsecret"`}
		}
		o := newShadowObservation(recommendOptions{role: "developer", taskClass: original}, spawnArguments{}, recommend.DefaultPolicy(), recommend.DecisionRecord{}, err, 0, at)
		if o.CMRError == nil || o.CMRError.Code != code || strings.Contains(o.CMRError.Message, "private-path") || strings.Contains(o.CMRError.Message, "provider-output") || strings.Contains(o.CMRError.Message, "\n") {
			t.Fatalf("%+v", o)
		}
		if code == "invalid_task" && o.CMRError.Message != `unknown class "x\nsecret"` {
			t.Fatal(o.CMRError)
		}
		if err := appendShadowObservation(cmrio.StateRoot(), o); err != nil {
			t.Fatal(err)
		}
	}
	report, err := readShadowReport(cmrio.StateRoot(), nil)
	if err != nil || report.Totals.FailOpenLaunches != 4 || len(report.FailOpenByCode) != 3 || report.FailOpenByCode[0] != (shadowRefusalCount{Code: "preflight_timeout", Count: 2}) {
		t.Fatal(report, err)
	}
	var human bytes.Buffer
	if err := renderShadowReport(&human, report); err != nil || !strings.Contains(human.String(), "preflight_timeout: 2") {
		t.Fatal(human.String(), err)
	}
	unknown := sanitizedShadowError(errors.New("private marker"), "code")
	if strings.Contains(unknown.Message, "private") {
		t.Fatal(unknown)
	}
}

func TestTimeoutFlagsValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--preflight-timeout", "0s"}, {"--preflight-timeout", "-1s"}, {"--preflight-timeout", "25h"}, {"--advisory-timeout", "0s"},
	} {
		if _, err := recommendFlags("spawn", args, true); err == nil {
			t.Fatal("bad timeout accepted", args)
		}
	}
}

func TestPreflightTimeoutFlagOverridesPolicy(t *testing.T) {
	root := cliEnvironment(t)
	policy := filepath.Join(root, "policy.toml")
	if err := os.WriteFile(policy, []byte("preflight_timeout_seconds = 1"), 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
/bin/sleep 0.2
printf '%s\n' '{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":false}}'
`
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		timeout  string
		code     int
		contains string
	}{
		{"", 0, "sha256:"}, {"50ms", 2, "preflight_timeout"}, {"500ms", 0, "sha256:"},
	} {
		args := []string{"recommend", "--policy", policy, "--role", "developer", "--task-class", "code", "--host", "fictional", "--json"}
		if tc.timeout != "" {
			args = append(args, "--preflight-timeout", tc.timeout)
		}
		var out, stderr bytes.Buffer
		if code := run(args, &out, &stderr); code != tc.code || !strings.Contains(out.String(), tc.contains) {
			t.Fatal(tc, code, out.String(), stderr.String())
		}
	}
}
