package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/internal/cmrio"
)

func TestShadowFailOpen(t *testing.T) {
	for _, policyMode := range []bool{false, true} {
		for _, failure := range []string{"success", "invalid_admission", "preflight_error", "refusal", "no_qualified_candidate", "policy_validation", "policy_decode", "policy_conversion", "policy_syntax", "usage_error", "catalog_error", "decision_write", "forwarded_parse", "conflicting_locks"} {
			name := "flag/"
			if policyMode {
				name = "policy/"
			}
			t.Run(name+failure, func(t *testing.T) {
				root := cliEnvironment(t)
				file := candidateFile(t, root, false)
				log := fakeTaskBoard(t, root)
				t.Setenv("FAKE_EXIT", "23")
				policy := filepath.Join(root, "policy.toml")
				body := ""
				if policyMode {
					body = "mode = 'shadow'\n"
				}
				forwarded := []string{"TASK with spaces", "--role", "developer", "--background", "--board-dir=board with spaces", "--", "--model=literal", ""}
				options := []string{"--role", "developer", "--task-class", "code.implement"}
				switch failure {
				case "invalid_admission":
					if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
				case "preflight_error":
					file = "" // The fake board exits 23 on the preflight query.
				case "refusal":
					options = append(options, "--difficulty", "invalid")
				case "no_qualified_candidate":
					file = candidateFile(t, root, true)
				case "policy_validation":
					body += "fanout_k = 0\n"
				case "policy_decode":
					body += "unknown = true\n"
				case "policy_conversion":
					body += "standard_a_max_cost_ratio = nan\n"
				case "policy_syntax":
					body += "fanout_k = [\n"
				case "usage_error":
					t.Setenv("CODEX_HOME", "relative-home")
				case "catalog_error":
					options = append(options, "--catalog", filepath.Join(root, "missing-catalog"))
				case "decision_write":
					if err := os.MkdirAll(cmrio.StateRoot(), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(cmrio.StateRoot(), "decisions"), []byte("blocked"), 0600); err != nil {
						t.Fatal(err)
					}
				case "forwarded_parse":
					forwarded = []string{"TASK", "--unknown", "--model"}
				case "conflicting_locks":
					options = append(options, "--agent", "codex")
					forwarded = []string{"TASK", "--agent=claude"}
				}
				if err := os.WriteFile(policy, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				options = append(options, "--policy", policy)
				if !policyMode {
					options = append(options, "--mode", "shadow")
				}
				if file != "" {
					options = append(options, "--candidates", file)
				}
				args := append([]string{"spawn"}, options...)
				args = append(args, "--json", "--")
				args = append(args, forwarded...)
				var out, stderr bytes.Buffer
				if code := run(args, &out, &stderr); code != 23 {
					t.Fatal("shadow did not return task-board status", code, out.String(), stderr.String())
				}
				b, err := os.ReadFile(log)
				if err != nil {
					t.Fatal("shadow did not launch", err)
				}
				got := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
				// Preflight is advisory; the final call must be precisely the original spawn.
				want := append([]string{"spawn"}, forwarded...)
				if len(got) < len(want) || !reflect.DeepEqual(got[len(got)-len(want):], want) || strings.Count(string(b), "spawn\n") != 1 {
					t.Fatal("shadow changed spawn argv", got, want)
				}
				if out.Len() != 0 || !strings.HasPrefix(stderr.String(), "cmr:shadow ") {
					t.Fatal("recommendation leaked into board output or missing shadow log", out.String(), stderr.String())
				}
				if failure == "success" {
					if !strings.Contains(stderr.String(), "sha256:") {
						t.Fatal("missing would-be decision", stderr.String())
					}
				} else {
					wantError := map[string]string{"invalid_admission": "invalid_admission", "preflight_error": "spawn-preflight failed", "refusal": "invalid_task", "no_qualified_candidate": "no_qualified_candidate", "policy_validation": "invalid_policy", "policy_decode": "invalid_policy", "policy_conversion": "invalid_policy", "policy_syntax": "invalid_policy", "usage_error": "cmr_usage_", "catalog_error": "invalid_catalog", "decision_write": "cmr_decision_write_failed", "forwarded_parse": "cmr_invalid_arguments", "conflicting_locks": "conflicting --agent"}[failure]
					if !strings.Contains(stderr.String(), wantError) {
						t.Fatal("missing advisory failure", wantError, stderr.String())
					}
				}
			})
		}
	}
}

type brokenShadowLog struct{}

func (brokenShadowLog) Write([]byte) (int, error) { return 0, errors.New("log unavailable") }

func TestShadowUnavailableLogAndPolicy(t *testing.T) {
	root := cliEnvironment(t)
	log := fakeTaskBoard(t, root)
	t.Setenv("FAKE_EXIT", "19")
	args := []string{"spawn", "--mode", "shadow", "--policy", filepath.Join(root, "missing.toml"), "--", "TASK"}
	var out bytes.Buffer
	if code := run(args, &out, brokenShadowLog{}); code != 19 {
		t.Fatal("logging/policy failure blocked shadow", code, out.String())
	}
	b, err := os.ReadFile(log)
	if err != nil || string(b) != "spawn\nTASK\n" {
		t.Fatal(string(b), err)
	}
}

func TestShadowOutputCopyPreservesChildStatus(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		for _, status := range []int{0, 23} {
			t.Run(fmt.Sprintf("%s/%d", stream, status), func(t *testing.T) {
				root := cliEnvironment(t)
				binary := filepath.Join(root, "task-board")
				body := fmt.Sprintf("#!/bin/sh\nprintf 'child output\\n' %s\nexit %d\n", map[string]string{"stdout": "", "stderr": ">&2"}[stream], status)
				if err := os.WriteFile(binary, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				var stdout, stderr io.Writer = &output, &output
				if stream == "stdout" {
					stdout = brokenShadowLog{}
				} else {
					stderr = brokenShadowLog{}
				}
				args := []string{"spawn", "--mode", "shadow", "--policy", filepath.Join(root, "missing.toml"), "--", "TASK"}
				if code := run(args, stdout, stderr); code != status {
					t.Fatalf("child exited %d, wrapper returned %d: %s", status, code, output.String())
				}
			})
		}
	}
}

func TestSpawnInvalidPolicyModePrecedence(t *testing.T) {
	for _, body := range []string{"mode = 'shadow'\nfanout_k = [", "mode = 'shadow'\nstandard_a_max_cost_ratio = nan"} {
		for _, mode := range []string{"select", "recommend"} {
			root := cliEnvironment(t)
			log := fakeTaskBoard(t, root)
			policy := filepath.Join(root, "policy.toml")
			if err := os.WriteFile(policy, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			if code := run([]string{"spawn", "--mode", mode, "--policy", policy, "--json", "--", "TASK"}, &out, &stderr); code != 2 || !strings.Contains(out.String(), "invalid_policy") {
				t.Fatal(mode, code, out.String(), stderr.String())
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("select/recommend launched invalid policy")
			}
		}
	}
}

func TestShadowOnlyWrapperParseAndMissingBoardStopLaunch(t *testing.T) {
	for _, args := range [][]string{
		{"spawn", "--mode", "shadow", "--unknown", "--", "TASK"},
		{"spawn", "--mode", "shadow", "--budget", "invalid", "--", "TASK"},
		{"spawn", "--mode", "shadow", "--json=invalid", "--", "TASK"},
		{"spawn", "--mode", "shadow"},
	} {
		root := cliEnvironment(t)
		log := fakeTaskBoard(t, root)
		var out, stderr bytes.Buffer
		if code := run(args, &out, &stderr); code != 2 {
			t.Fatal(code, out.String(), stderr.String())
		}
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Fatal("invalid wrapper flags launched task-board")
		}
	}
	cliEnvironment(t)
	var out, stderr bytes.Buffer
	if code := run([]string{"spawn", "--mode", "shadow", "--", "TASK"}, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "cmr_task_board_unavailable") {
		t.Fatal(code, out.String(), stderr.String())
	}
}

func TestShadowRawModeAndUnknownPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, body, flag string
		status           int
	}{
		{"json-syntax", `{"mode":"shadow","fanout_k":`, "", 23},
		{"json-flag-select", `{"mode":"shadow","fanout_k":`, "select", 2},
		{"json-flag-recommend", `{"mode":"shadow","fanout_k":`, "recommend", 2},
		{"missing-policy-unknown", "", "", 2},
		{"missing-policy-shadow", "", "shadow", 23},
		{"missing-policy-select", "", "select", 2},
		{"missing-policy-recommend", "", "recommend", 2},
		{"nested-mode", `{"rules":{"mode":"shadow"},"fanout_k":`, "", 2},
		{"flag-overrides-raw", `{"mode":"select","fanout_k":`, "shadow", 23},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliEnvironment(t)
			log := fakeTaskBoard(t, root)
			t.Setenv("FAKE_EXIT", "23")
			policy := filepath.Join(root, "policy.json")
			if tc.body != "" {
				if err := os.WriteFile(policy, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"spawn", "--policy", policy, "--json"}
			if tc.flag != "" {
				args = append(args, "--mode", tc.flag)
			}
			args = append(args, "--", "TASK with spaces", "")
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != tc.status {
				t.Fatal(code, out.String(), stderr.String())
			}
			b, err := os.ReadFile(log)
			if tc.status == 23 {
				if err != nil || string(b) != "spawn\nTASK with spaces\n\n" || !strings.Contains(stderr.String(), "invalid_policy") {
					t.Fatal("raw mode did not forward exact original launch", string(b), err, stderr.String())
				}
			} else if !os.IsNotExist(err) || !strings.Contains(out.String(), "invalid_policy") {
				t.Fatal("invalid/unknown policy launched", string(b), err, out.String())
			}
		})
	}
}

func TestShadowStartFailureRefuses(t *testing.T) {
	root := cliEnvironment(t)
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte("#!/missing/interpreter\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"spawn", "--mode", "shadow", "--policy", filepath.Join(root, "missing.toml"), "--json", "--", "TASK"}, &out, &stderr); code != 2 || !strings.Contains(out.String(), "cmr_spawn_failed") {
		t.Fatal(code, out.String(), stderr.String())
	}
}
