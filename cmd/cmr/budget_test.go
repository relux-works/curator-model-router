package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestBudgetCLIOverrideAndSpawn(t *testing.T) {
	for _, command := range []string{"recommend", "spawn"} {
		for _, mode := range []string{"economy", "balanced", "burn"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				root := cliEnvironment(t)
				file := candidateFile(t, root, false)
				policy := filepath.Join(root, "policy.toml")
				if err := os.WriteFile(policy, []byte("budget_mode = 'economy'\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{command, "--role", "developer", "--task-class", "code.implement", "--difficulty", "standard", "--policy", policy, "--candidates", file, "--budget", mode, "--json"}
				log := ""
				if command == "spawn" {
					log = fakeTaskBoard(t, root)
					args = append(args, "--", "TASK")
				}
				var out, stderr bytes.Buffer
				if code := run(args, &out, &stderr); code != 0 {
					t.Fatal(code, out.String(), stderr.String())
				}
				paths, err := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
				if err != nil || len(paths) != 1 {
					t.Fatal(paths, err)
				}
				raw, err := os.ReadFile(paths[0])
				if err != nil {
					t.Fatal(err)
				}
				d, err := recommend.LoadDecision(raw)
				if err != nil {
					t.Fatal(err)
				}
				if d.Inputs.Policy.BudgetMode != mode {
					t.Fatal("override not recorded", d.Inputs.Policy)
				}
				if command == "spawn" {
					raw, err = os.ReadFile(log)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(raw), "--budget") || !strings.Contains(string(raw), d.Recommendation.Selected.Model) {
						t.Fatal(string(raw))
					}
				} else {
					var result recommendOutput
					if err = json.Unmarshal(out.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if result.Selected == nil || *result.Selected != *d.Recommendation.Selected {
						t.Fatal(result)
					}
				}
			})
		}
	}
}

func TestShippedHostReviewScope(t *testing.T) {
	for _, difficulty := range []string{"trivial", "routine", "standard", "hard", "critical"} {
		for _, delicate := range []bool{false, true} {
			root := cliEnvironment(t)
			file := candidateFile(t, root, false)
			args := []string{"recommend", "--role", "reviewer", "--task-class", "review.code", "--difficulty", difficulty, "--candidates", file, "--policy", "../../docs/standing-rules.toml", "--host", "<your-build-host>", "--producer-family", "openai", "--json"}
			if delicate {
				args = append(args, "--delicate")
			}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			var got recommendOutput
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Selected == nil {
				t.Fatal(got)
			}
			if !delicate && difficulty != "hard" && difficulty != "critical" {
				if *got.Selected != (recommend.Candidate{Runtime: "codex", Model: "gpt-6-astra", Effort: "medium"}) {
					t.Fatal(got)
				}
			} else {
				if got.Selected.Runtime != "claude" || got.Selected.Model != "claude-sonnet-5-5" || got.Selected.Effort != "max" {
					t.Fatal("demanding review did not preserve best quality", got)
				}
				if len(got.SkippedRules) == 0 || got.Selected.Effort == "medium" && got.Selected.Model == "gpt-6-astra" {
					t.Fatal(got)
				}
			}
		}
	}
}

func TestInvalidBudgetDoesNotQueryOrLaunch(t *testing.T) {
	root := cliEnvironment(t)
	log := fakeTaskBoard(t, root)
	for _, command := range []string{"recommend", "spawn"} {
		args := []string{command, "--role", "developer", "--task-class", "code.implement", "--budget", "invalid", "--json"}
		if command == "spawn" {
			args = append(args, "--", "TASK")
		}
		var out, stderr bytes.Buffer
		if code := run(args, &out, &stderr); code != 2 || !strings.Contains(out.String(), "cmr_invalid_arguments") {
			t.Fatal(code, out.String(), stderr.String())
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("invalid budget invoked board")
	}
}
