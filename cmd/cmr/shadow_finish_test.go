package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
)

// The test binary is not cmr: run the deferred advisory through the process
// helper, which calls run() with the same arguments.
func init() {
	shadowFinishCommand = func(args []string) (*exec.Cmd, error) {
		// Tests of the parent's own cancellation and reaping opt out of hand-off.
		if os.Getenv("CMR_TEST_NO_DEFER") == "1" {
			return nil, errors.New("hand-off disabled")
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestShadowProcessHelper$")
		cmd.Env = append(os.Environ(), "CMR_PROCESS_HELPER=1", "CMR_PROCESS_ARGS="+string(encoded))
		return cmd, nil
	}
}

func waitShadowObservations(t *testing.T, n int) []shadowObservation {
	t.Helper()
	path := shadowObservationPath(cmrio.StateRoot())
	deadline := time.Now().Add(15 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil && bytes.Count(data, []byte{'\n'}) >= n {
			return readShadowObservations(t)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no deferred observation within the deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestShadowFastChildDefersAdvisory(t *testing.T) {
	root := cliEnvironment(t)
	t.Setenv("FAKE_LOG", filepath.Join(root, "argv"))
	// A background spawn returns at once; the board preflight answers later.
	script := `#!/bin/sh
/usr/bin/printf '%s\n' "$@" >> "$FAKE_LOG"
if [ "$1" = '--no-update-check' ]; then
/bin/sleep 1
/bin/cat <<'PAYLOAD'
{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":true,"contract_version":"spawn-policy-v2","model_criterion":"less_or_equal","admitted_pairs":{"provider":"codex","models":[{"id":"gpt-6.1-sol","efforts":["high"]}]}},"workload_class_recommendation":{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":[{"runtime":"codex","model":"gpt-6.1-sol","reasoning_effort":"high"}]}}
PAYLOAD
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	started := time.Now()
	code := run([]string{"spawn", "--mode", "shadow", "--task-class", "code", "--", "TASK", "--role=developer", "--agent=muse", "--model=muse-spark-1.3-contributor", "--reasoning-effort=max", "--background"}, &out, &stderr)
	if code != 0 {
		t.Fatal(code, stderr.String())
	}
	if time.Since(started) > 900*time.Millisecond {
		t.Fatal("wrapper waited for the slow preflight")
	}
	if !strings.Contains(stderr.String(), "cmr:shadow advisory deferred to pid ") {
		t.Fatal("advisory was not deferred", stderr.String())
	}
	o := waitShadowObservations(t, 1)
	if len(o) != 1 {
		t.Fatalf("parent and deferred advisory both recorded: %+v", o)
	}
	if o[0].CMRError != nil || o[0].CMRPick != (shadowPick{Runtime: "codex", Model: "gpt-6.1-sol", Effort: "high"}) || o[0].Agreement != "different_runtime" || o[0].TaskBoardExitCode != 0 || o[0].OriginalTaskClass != "code" || o[0].Budget != "balanced" {
		t.Fatalf("%+v", o[0])
	}
	raw, _ := os.ReadFile(filepath.Join(root, "argv"))
	if strings.Count(string(raw), "spawn\nTASK\n") != 1 {
		t.Fatal("the deferred advisory launched task-board", string(raw))
	}
}

func TestShadowFinishIsBoundedAndNeverLaunches(t *testing.T) {
	root := cliEnvironment(t)
	t.Setenv("FAKE_LOG", filepath.Join(root, "argv"))
	script := "#!/bin/sh\n/usr/bin/printf '%s\\n' \"$@\" >> \"$FAKE_LOG\"\nexec /bin/sleep 30\n"
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var out, stderr bytes.Buffer
	if code := run([]string{"shadow-finish", "--exit-status", "5", "--task-class", "code", "--advisory-timeout", "300ms", "--", "TASK", "--role=developer", "--background"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("deferred advisory outlived its deadline")
	}
	o := readShadowObservations(t)
	if len(o) != 1 || o[0].CMRError == nil || o[0].TaskBoardExitCode != 5 || o[0].Agreement != "no_cmr_pick" {
		t.Fatalf("%+v", o)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "argv"))
	if strings.Contains(string(raw), "spawn\n") {
		t.Fatal("shadow-finish launched task-board", string(raw))
	}
}

func TestShadowDeferredAdvisoryGetsBudgetFloor(t *testing.T) {
	original := shadowFinishCommand
	t.Cleanup(func() { shadowFinishCommand = original })
	var got []string
	shadowFinishCommand = func(args []string) (*exec.Cmd, error) {
		got = args
		return exec.Command("/usr/bin/true"), nil
	}
	timeoutArg := func() time.Duration {
		t.Helper()
		var last time.Duration
		for i, a := range got {
			if a == "--" {
				break
			}
			if a == "--advisory-timeout" {
				d, err := time.ParseDuration(got[i+1])
				if err != nil {
					t.Fatal(err)
				}
				last = d
			}
		}
		return last
	}
	wrapper := []string{"--task-class", "code", "--advisory-timeout", "60s"}
	// A background spawn leaves about the whole launch deadline; the floor wins.
	if deferShadowAdvice(wrapper, []string{"TASK"}, 0, 59*time.Second) == 0 {
		t.Fatal("hand-off failed")
	}
	if d := timeoutArg(); d != deferredAdvisoryBudget {
		t.Fatalf("deferred budget %v, want %v: %q", d, deferredAdvisoryBudget, got)
	}
	// A caller deadline longer than the floor is kept.
	if deferShadowAdvice(wrapper, []string{"TASK"}, 0, 5*time.Minute) == 0 {
		t.Fatal("hand-off failed")
	}
	if d := timeoutArg(); d != 5*time.Minute {
		t.Fatalf("deferred budget %v, want 5m: %q", d, got)
	}
	// An expired deadline means the advice already had its time: no hand-off.
	got = nil
	if pid := deferShadowAdvice(wrapper, []string{"TASK"}, 0, 0); pid != 0 || got != nil {
		t.Fatalf("expired deadline handed off: pid %d %q", pid, got)
	}
}
