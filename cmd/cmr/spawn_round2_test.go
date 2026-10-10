package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestSpawnPreflightTargetsSameBoard(t *testing.T) {
	for _, tc := range []struct {
		name          string
		selectors     []string
		model, effort string
	}{
		{"board-A", []string{"--board-dir", "/board-A"}, "gpt-6-astra", "medium"},
		{"board-B", []string{"--board-dir=/board-B"}, "gpt-6.1-sol", "high"},
		{"remote-B", []string{"--remote", "https://example.invalid", "--remote-board=board-B", "--insecure=false"}, "gpt-6.1-sol", "high"},
		{"literal-board-value", []string{"--board-dir", "--"}, "gpt-6.1-sol", "high"},
	} {
		for _, incomplete := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/incomplete=%t", tc.name, incomplete), func(t *testing.T) {
				root := cliEnvironment(t)
				log := filepath.Join(root, "argv")
				t.Setenv("FAKE_LOG", log)
				if incomplete {
					t.Setenv("FAKE_INCOMPLETE", "1")
				} else {
					t.Setenv("FAKE_INCOMPLETE", "0")
				}
				// A different board has a different admitted pair. Default preflight is A.
				script := `#!/bin/sh
printf '%s\n' "$@" >> "$FAKE_LOG"
[ "$1" = 'spawn' ] && exit 0
if [ "$FAKE_INCOMPLETE" = 1 ]; then
 case "$*" in
 *agent=*) ;;
 *) printf '%s\n' '{"enabled":true,"role":"developer","providers":{"allowed":["codex"]}}'; exit 0 ;;
 esac
fi
model=gpt-6-astra
effort=medium
while [ "$#" -gt 0 ]; do
 case "$1" in
 '--board-dir') shift; [ "$1" = '/board-A' ] || { model=gpt-6.1-sol; effort=high; } ;;
 '--board-dir=/board-B'|'--remote-board=board-B') model=gpt-6.1-sol; effort=high ;;
 esac
 shift
done
printf '{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":true,"admitted_pairs":{"provider":"codex","models":[{"id":"%s","efforts":["%s"]}]}},"workload_class_recommendation":{"configured":true,"class_resolved":false,"unresolved_reason":"workload_class_derivation_input_required"}}\n' "$model" "$effort"
`
				if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				forwarded := append([]string{"TASK", "--background"}, tc.selectors...)
				args := append([]string{"spawn", "--role", "developer", "--task-class", "code.implement", "--mode", "recommend", "--json", "--"}, forwarded...)
				var out, stderr bytes.Buffer
				if code := run(args, &out, &stderr); code != 0 {
					t.Fatal(code, out.String(), stderr.String())
				}
				var result recommendOutput
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Selected == nil || result.Selected.Model != tc.model || result.Selected.Effort != tc.effort {
					t.Fatal("selected from wrong board", result.Selected)
				}
				logged, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				expected := "--no-update-check\n" + strings.Join(tc.selectors, "\n") + "\nq\n"
				queries := 1
				if incomplete {
					queries = 2
					if !strings.Contains(string(logged), "project_config(view=spawn-preflight, role=developer, agent=codex)") {
						t.Fatal("missing targeted fallback", string(logged))
					}
				}
				if strings.Count(string(logged), expected) != queries {
					t.Fatal("wrong query count or missing board target", string(logged))
				}
				if !reflect.DeepEqual(result.Commands[0][2:2+len(forwarded)], forwarded) {
					t.Fatal(result.Commands)
				}
				paths, _ := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
				data, _ := os.ReadFile(paths[0])
				var record recommend.DecisionRecord
				if err := json.Unmarshal(data, &record); err != nil || record.Inputs.Admission == nil || !reflect.DeepEqual(record.Inputs.Admission.BoardFlags, tc.selectors) {
					t.Fatal(record.Inputs.Admission, err)
				}
				if err := record.VerifyContentID(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestSpawnSelectionConfirmationContract(t *testing.T) {
	for _, tc := range []struct {
		name, ceiling string
		required      bool
	}{
		{"unconfigured", `"configured":false`, false},
		{"absent-criterion", `"configured":true`, false},
		{"equal", `"configured":true,"contract_version":"spawn-policy-v2","model_criterion":"equal"`, false},
		{"ordered-lower", `"configured":true,"contract_version":"spawn-policy-v2","model_criterion":"less_or_equal"`, true},
		{"ordered-upper", `"configured":true,"model_criterion":"greater_or_equal"`, true},
		{"v3-required", `"configured":true,"contract_version":"spawn-policy-v3","adjustment_confirmation":"required"`, true},
		{"v3-none", `"configured":true,"contract_version":"spawn-policy-v3","adjustment_confirmation":"none"`, false},
		{"v3-no-provider-allow-set", `"configured":false,"contract_version":"spawn-policy-v3"`, false},
		{"v4-required", `"configured":true,"contract_version":"spawn-policy-v4","adjustment_confirmation":"required"`, true},
		{"v4-none", `"configured":true,"contract_version":"spawn-policy-v4","adjustment_confirmation":"none"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliEnvironment(t)
			log := filepath.Join(root, "argv")
			t.Setenv("FAKE_LOG", log)
			required := 0
			if tc.required {
				required = 1
			}
			body := `{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{` + tc.ceiling + `,"admitted_pairs":{"provider":"codex","models":[{"id":"gpt-6.1-sol","efforts":["high"]}]}},"workload_class_recommendation":{"configured":true,"class_resolved":false,"unresolved_reason":"workload_class_derivation_input_required"}}`
			// Faithful unchanged-pair confirmation gate: required needs one nonempty
			// one-line rationale, every other policy rejects an injected rationale.
			script := fmt.Sprintf(`#!/bin/sh
printf 'CALL\n%%s\n' "$@" >> "$FAKE_LOG"
if [ "$1" != 'spawn' ]; then
/bin/cat <<'PAYLOAD'
%s
PAYLOAD
exit 0
fi
count=0
while [ "$#" -gt 0 ]; do
 if [ "$1" = '--selection-rationale' ]; then
  shift
  [ -n "$1" ] || exit 91
  case "$1" in *'
'*) exit 92 ;; esac
  count=$((count+1))
 fi
 shift
done
[ "$count" -eq %d ] || exit 93
`, body, required)
			if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			if code := run([]string{"spawn", "--role", "developer", "--task-class", "code.implement", "--agent", "codex", "--model", "sol", "--reasoning-effort", "high", "--", "TASK", "--background"}, &out, &stderr); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "cmr:sha256:") {
				t.Fatal("cmr lost decision id", stderr.String())
			}
			paths, _ := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
			data, _ := os.ReadFile(paths[0])
			var record recommend.DecisionRecord
			if err := json.Unmarshal(data, &record); err != nil || record.DecisionID == "" || record.Inputs.Admission.RationaleRequired["codex"] != tc.required {
				t.Fatal("lost confirmation policy/decision", err)
			}
		})
	}
}

func TestModelAliasMatchingPreservesArgv(t *testing.T) {
	for alias, canonical := range map[string]string{"sol": "gpt-6.1-sol", "astra": "gpt-6-astra", "luna": "gpt-6-luna", "opus": "claude-opus-5-5", "sonnet": "claude-sonnet-5-5", "gemini-flash": "gemini-3.8-flash-high", "muse-spark": "muse-spark-1.3-contributor"} {
		if got := canonicalModel(alias); got != canonical {
			t.Fatal(alias, got)
		}
	}
	for _, model := range []string{"sol", "gpt-6.1-sol"} {
		t.Run(model, func(t *testing.T) {
			root := cliEnvironment(t)
			file := filepath.Join(root, "candidates.json")
			if err := os.WriteFile(file, []byte(`[{"runtime":"codex","model":"gpt-6.1-sol","effort":"high"}]`), 0600); err != nil {
				t.Fatal(err)
			}
			forwarded := []string{"TASK", "--model", model, "--agent=codex", "--reasoning-effort", "high", "--background"}
			args := append([]string{"spawn", "--role", "developer", "--task-class", "code.implement", "--model", "gpt-6.1-sol", "--candidates", file, "--mode", "recommend", "--json", "--"}, forwarded...)
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			var result recommendOutput
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Selected == nil || result.Selected.Model != "gpt-6.1-sol" || result.Selected.Effort != "high" || !reflect.DeepEqual(result.Commands[0][2:2+len(forwarded)], forwarded) {
				t.Fatal(result)
			}
			// Alias normalization cannot manufacture admission for the target model.
			if err := os.WriteFile(file, []byte(`[{"runtime":"codex","model":"gpt-6-astra","effort":"medium"}]`), 0600); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			stderr.Reset()
			if code := run(args, &out, &stderr); code != 2 || !strings.Contains(out.String(), "no_qualified_candidate") {
				t.Fatal("alias widened admission", code, out.String(), stderr.String())
			}
		})
	}
}

func TestConfirmationPerProviderAndCallerRationale(t *testing.T) {
	codex := recommend.Candidate{Runtime: "codex", Model: "gpt-6.1-sol", Effort: "high"}
	agy := recommend.Candidate{Runtime: "agy", Model: "gemini-3.8-flash-high", Effort: "none"}
	r := recommend.DecisionRecord{DecisionID: "sha256:test", Inputs: recommend.Request{Task: recommend.TaskProfile{Role: "researcher", TaskClass: "research", Pipeline: "fanout"}, Admission: &recommend.AdmissionContext{RationaleRequired: map[string]bool{"codex": true, "agy": false}}}, Recommendation: recommend.Recommendation{Selected: &codex, FanOut: []recommend.Candidate{codex, agy}}}
	args := []string{"TASK", "--background"}
	parsed, err := parseSpawnArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	commands := spawnCommands(r, args, parsed, "select")
	if len(commands) != 2 || !strings.Contains(strings.Join(commands[0], "\n"), "--selection-rationale\ncmr:sha256:test") || strings.Contains(strings.Join(commands[1], "\n"), "--selection-rationale") {
		t.Fatal(commands)
	}
	r.Inputs.Task.Pipeline = "single"
	args = append(args, "--selection-rationale", "caller rationale")
	parsed, err = parseSpawnArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	commands = spawnCommands(r, args, parsed, "select")
	if strings.Count(strings.Join(commands[0], "\n"), "--selection-rationale") != 1 || !reflect.DeepEqual(commands[0][2:2+len(args)], args) {
		t.Fatal("overwrote caller rationale", commands)
	}
}
