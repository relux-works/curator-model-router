package main

import (
	"bytes"
	"encoding/json"
	"github.com/relux-works/curator-model-router/pkg/recommend"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fakeTaskBoard(t *testing.T, root string) string {
	t.Helper()
	log := filepath.Join(root, "spawn-argv")
	t.Setenv("FAKE_LOG", log)
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$FAKE_LOG\"\nexit \"${FAKE_EXIT:-0}\"\n"
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return log
}
func TestSpawnModesLocksAndRefusal(t *testing.T) {
	for _, mode := range []string{"select", "recommend", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			root := cliEnvironment(t)
			file := candidateFile(t, root, false)
			log := fakeTaskBoard(t, root)
			forwarded := []string{"task-example", "--role", "developer", "--agent=codex", "--model", "gpt-6.1-sol", "--reasoning-effort=high", "--background", "--task-path", "task with spaces"}
			args := []string{"spawn", "--task-class", "code.implement", "--candidates", file, "--mode", mode, "--json", "--"}
			args = append(args, forwarded...)
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			b, err := os.ReadFile(log)
			if mode == "recommend" {
				if !os.IsNotExist(err) {
					t.Fatal("recommend launched")
				}
				var result recommendOutput
				if err = json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Commands) != 1 {
					t.Fatal(result, err)
				}
				if !reflect.DeepEqual(result.Commands[0][2:2+len(forwarded)], forwarded) {
					t.Fatal(result.Commands)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
			want := append([]string{"spawn"}, forwarded...)
			if mode == "shadow" {
				if !reflect.DeepEqual(got, want) || !strings.Contains(stderr.String(), "shadow") {
					t.Fatal(got, stderr.String())
				}
			} else {
				if !reflect.DeepEqual(got, want) || !strings.Contains(stderr.String(), "cmr:sha256:") {
					t.Fatal(got)
				}
			}
		})
	}
	root := cliEnvironment(t)
	empty := candidateFile(t, root, true)
	log := fakeTaskBoard(t, root)
	for _, mode := range []string{"select", "recommend"} {
		var out, stderr bytes.Buffer
		if code := run([]string{"spawn", "--role", "developer", "--task-class", "code.implement", "--candidates", empty, "--mode", mode, "--json", "--", "TASK"}, &out, &stderr); code != 2 || !strings.Contains(out.String(), "no_qualified_candidate") {
			t.Fatal(code, out.String())
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("refusal launched")
	}
}
func TestFanoutAndTaskBoardStatus(t *testing.T) {
	root := cliEnvironment(t)
	file := candidateFile(t, root, false)
	log := fakeTaskBoard(t, root)
	args := []string{"spawn", "--role", "researcher", "--task-class", "research", "--candidates", file, "--fanout", "--", "TASK", "--background", "--task-path", "hello"}
	var out, stderr bytes.Buffer
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	b, _ := os.ReadFile(log)
	if strings.Count(string(b), "spawn\n") != 2 {
		t.Fatalf("expected catalog's two distinct A-or-better families: %s", b)
	}
	// Each fan-out launch must request a parallel run, or task-board returns the
	// existing live run and only the first family would actually start.
	if strings.Count(string(b), "--allow-parallel\n") != 2 {
		t.Fatalf("fan-out launches must pass --allow-parallel: %s", b)
	}
	os.Remove(log)
	// A caller-supplied --allow-parallel is not duplicated.
	args = []string{"spawn", "--role", "researcher", "--task-class", "research", "--candidates", file, "--fanout", "--", "TASK", "--background", "--allow-parallel"}
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	b, _ = os.ReadFile(log)
	if strings.Count(string(b), "--allow-parallel\n") != 2 {
		t.Fatalf("caller --allow-parallel must appear once per launch: %s", b)
	}
	os.Remove(log)
	t.Setenv("FAKE_EXIT", "23")
	args = []string{"spawn", "--role", "developer", "--task-class", "code.implement", "--candidates", file, "--", "TASK", "--", "literal"}
	if code := run(args, &out, &stderr); code != 23 {
		t.Fatal(code, out.String(), stderr.String())
	}
	b, _ = os.ReadFile(log)
	if strings.Contains(string(b), "--selection-rationale\n") || !strings.HasSuffix(string(b), "--\nliteral\n") {
		t.Fatal(string(b))
	}
}
func TestSpawnInvalidLocks(t *testing.T) {
	root := cliEnvironment(t)
	file := candidateFile(t, root, false)
	log := fakeTaskBoard(t, root)
	for _, forwarded := range [][]string{{"--model"}, {"--agent=codex", "--agent", "codex"}, {"--agent=claude"}, {"TASK", "--prompt", "unsupported"}, {"TASK", "--unknown", "--model=literal"}, {"TASK", "-x"}} {
		args := []string{"spawn", "--role", "developer", "--task-class", "code.implement", "--candidates", file, "--agent", "codex", "--json", "--"}
		args = append(args, forwarded...)
		var out, stderr bytes.Buffer
		if code := run(args, &out, &stderr); code != 2 || !strings.Contains(out.String(), "cmr_invalid_arguments") {
			t.Fatal(code, out.String(), stderr.String())
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("invalid locks launched")
	}
}

func TestSpawnNoEffortAxis(t *testing.T) {
	c := recommend.Candidate{Runtime: "agy", Model: "gemini-3.8-flash-high", Effort: "none"}
	flags := spawnFlags(c)
	if !reflect.DeepEqual(flags, []string{"--agent", "agy", "--model", "gemini-3.8-flash-high"}) {
		t.Fatal(flags)
	}
}

func TestSpawnForwardsRoutingRole(t *testing.T) {
	root := cliEnvironment(t)
	file := candidateFile(t, root, false)
	log := fakeTaskBoard(t, root)
	var out, stderr bytes.Buffer
	if code := run([]string{"spawn", "--role", "reviewer", "--task-class", "review.code", "--candidates", file, "--", "TASK"}, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	b, err := os.ReadFile(log)
	if err != nil || strings.Count(string(b), "--role\nreviewer\n") != 1 {
		t.Fatalf("routing role missing from launch: %s (%v)", b, err)
	}
}

func TestInvalidSpawnModeDoesNotQuery(t *testing.T) {
	root := cliEnvironment(t)
	log := fakeTaskBoard(t, root)
	var out, stderr bytes.Buffer
	if code := run([]string{"spawn", "--role", "developer", "--task-class", "code.implement", "--mode", "invalid", "--json", "--", "TASK"}, &out, &stderr); code != 2 || !strings.Contains(out.String(), "cmr_invalid_arguments") {
		t.Fatal(code, out.String(), stderr.String())
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("invalid mode queried task-board")
	}
}
