package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestSpawnArgumentArity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		locks    map[string]string
		boundary int
		invalid  bool
	}{
		{"selection rationale looks like model", []string{"TASK", "--role", "developer", "--background", "--selection-rationale", "--model=gpt-6.1-sol"}, map[string]string{"role": "developer"}, 6, false},
		{"recommendation rationale looks like model", []string{"TASK", "--recommendation-rationale", "--model=literal-rationale"}, map[string]string{}, 3, false},
		{"literal terminator value followed by locks", []string{"TASK", "--recommendation-rationale", "--", "--agent", "codex", "--model", "gpt-6.1-sol", "--reasoning-effort", "high"}, map[string]string{"agent": "codex", "model": "gpt-6.1-sol", "reasoning-effort": "high"}, 9, false},
		{"equals value and real terminator", []string{"TASK", "--context=--", "--", "--model=positional"}, map[string]string{}, 2, false},
		{"consumed flag is not a lock", []string{"TASK", "--instruction", "--agent", "--model=gpt-6.1-sol"}, map[string]string{"model": "gpt-6.1-sol"}, 4, false},
		{"boolean shorthands", []string{"TASK", "-h", "-v", "-vh", "--background=false", "--model=m"}, map[string]string{"model": "m"}, 6, false},
		{"unknown long", []string{"TASK", "--future", "--model=m"}, nil, 0, true},
		{"unknown shorthand", []string{"TASK", "-x"}, nil, 0, true},
		{"missing value", []string{"TASK", "--instruction"}, nil, 0, true},
		{"empty lock", []string{"TASK", "--model="}, nil, 0, true},
		{"duplicate lock", []string{"TASK", "--model=m", "--model", "m"}, nil, 0, true},
		{"invalid boolean", []string{"TASK", "--background=garbage"}, nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parseSpawnArgs(tc.args)
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted invalid arguments")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(p.locks, tc.locks) || p.injectionIndex != tc.boundary {
				t.Fatal(p, err)
			}
		})
	}
}

// Every registered value option consumes even option-looking values; boolean
// options (including Cobra help) leave the next token available as a real flag.
func TestSpawnFlagTable(t *testing.T) {
	values := strings.Fields("agent model reasoning-effort selection-rationale ack-resolved workload-class workload-task-class recommendation-rationale recommendation-snapshot-digest task-path dod-path instruction role timeout hard-timeout deadline-multiplier budget goal-scope context register-context board-dir remote remote-board context-profile")
	booleans := strings.Fields("background allow-parallel wait verbose no-context help json no-update-check insecure")
	if len(taskBoardSpawnFlags) != len(values)+len(booleans) {
		t.Fatal("unexpected flag table size")
	}
	for _, key := range values {
		t.Run(key, func(t *testing.T) {
			if takes, ok := taskBoardSpawnFlags[key]; !ok || !takes {
				t.Fatal("missing value arity")
			}
			for _, value := range []string{"--model=literal", "--"} {
				for _, args := range [][]string{{"TASK", "--" + key, value}, {"TASK", "--" + key + "=" + value}} {
					p, err := parseSpawnArgs(args)
					if err != nil || p.injectionIndex != len(args) {
						t.Fatal(args, p, err)
					}
					if key != "model" && p.locks["model"] != "" {
						t.Fatal("value mistaken for model flag")
					}
				}
			}
		})
	}
	for _, key := range booleans {
		if takes, ok := taskBoardSpawnFlags[key]; !ok || takes {
			t.Fatal(key, "missing boolean arity")
		}
		p, err := parseSpawnArgs([]string{"TASK", "--" + key, "--model=m"})
		if err != nil || p.locks["model"] != "m" {
			t.Fatal(key, p, err)
		}
	}
}

func TestSpawnAdversarialValuesPreserveLaunch(t *testing.T) {
	for _, tc := range []struct {
		name           string
		forwarded      []string
		injectionIndex int
	}{
		{"selection value", []string{"TASK", "--role", "developer", "--background", "--selection-rationale", "--model=gpt-6.1-sol"}, -1},
		{"recommendation value", []string{"TASK", "--role", "developer", "--background", "--recommendation-rationale", "--model=literal-rationale"}, -1},
		{"literal value before real locks", []string{"TASK", "--role", "developer", "--background", "--recommendation-rationale", "--", "--agent", "codex", "--model", "gpt-6.1-sol", "--reasoning-effort", "high"}, -1},
		{"literal value and terminator", []string{"TASK", "--role", "developer", "--background", "--selection-rationale", "--", "--", "--model=positional"}, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliEnvironment(t)
			file := candidateFile(t, root, false)
			log := fakeTaskBoard(t, root)
			args := []string{"spawn", "--task-class", "code.implement", "--candidates", file, "--json", "--"}
			var out, stderr bytes.Buffer
			if code := run(append(args, tc.forwarded...), &out, &stderr); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
			boundary := tc.injectionIndex
			if boundary < 0 {
				boundary = len(tc.forwarded)
			}
			// Expected boundaries come from the fixture, independently of the parser.
			prefix, suffix := tc.forwarded[:boundary], tc.forwarded[boundary:]
			if !reflect.DeepEqual(got[1:1+len(prefix)], prefix) || !reflect.DeepEqual(got[len(got)-len(suffix):], suffix) {
				t.Fatal("changed forwarded values", got)
			}
			paths, err := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
			if err != nil || len(paths) != 1 {
				t.Fatal(paths, err)
			}
			data, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			var record recommend.DecisionRecord
			if err = json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			c := record.Recommendation.Selected
			if c == nil {
				t.Fatal("missing selection")
			}
			// Fixtures use the separate-value form for every real routing flag.
			// A --model=... rationale or positional can never satisfy this check.
			for key, value := range map[string]string{"--agent": c.Runtime, "--model": c.Model, "--reasoning-effort": c.Effort} {
				if key == "--reasoning-effort" && value == "none" {
					continue
				}
				index := slices.Index(got, key)
				if index < 0 || index+1 >= len(got) || got[index+1] != value {
					t.Fatal("launch differs from decision", key, value, got)
				}
			}
		})
	}
}
