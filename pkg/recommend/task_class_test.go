package recommend

import (
	"errors"
	"testing"
)

func TestBoardTaskClassAliasesAndReplay(t *testing.T) {
	aliases := map[string]string{
		"implementation": "code.implement", "code": "code.implement", "unified": "code.implement",
		"debugging": "code.fix", "testing": "code.test", "migration": "code.refactor",
		"review": "review.code", "docs": "docs.write", "documentation": "docs.write",
		"research": "research", "architecture": "planning", "mechanical": "routine", "metadata": "routine", "operations": "ops",
	}
	cat, err := LoadCatalog(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	candidates := []Candidate{}
	for _, row := range cat.Rows {
		candidates = append(candidates, row.Candidate)
	}
	for original, mapped := range aliases {
		t.Run(original, func(t *testing.T) {
			task, err := (TaskProfile{Role: "developer", TaskClass: original}).Normalize()
			if err != nil || task.TaskClass != mapped || original != mapped && task.OriginalTaskClass != original {
				t.Fatal(task, err)
			}
			again, err := task.Normalize()
			if err != nil || again != task {
				t.Fatal("normalization changed provenance", again, err)
			}
			decision, err := BuildDecision(Request{Catalog: cat, Policy: DefaultPolicy(), Task: task, Candidates: candidates, Usage: UsageSnapshot{AsOf: 1}})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := decision.JSON()
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadDecision(raw)
			if err != nil || loaded.Inputs.Task != task {
				t.Fatal("alias decision does not replay", loaded.Inputs.Task, err)
			}
		})
	}
	orchestration, err := (TaskProfile{Role: "orchestrator", TaskClass: "operations"}).Normalize()
	if err != nil || orchestration.TaskClass != "orchestration" {
		t.Fatal(orchestration, err)
	}
	_, err = (TaskProfile{Role: "developer", TaskClass: "x"}).Normalize()
	var r *Refusal
	if !errors.As(err, &r) || r.Code != InvalidTask || r.Message != `unknown class "x"` {
		t.Fatal(err)
	}
	_, err = (TaskProfile{Role: "developer", TaskClass: "code.fix", OriginalTaskClass: "implementation"}).Normalize()
	if err == nil {
		t.Fatal("mismatched provenance accepted")
	}
}

func TestPreflightPolicyTimeout(t *testing.T) {
	for _, raw := range []string{`{"preflight_timeout_seconds":60}`, `{"preflight_timeout_seconds":120}`} {
		p, err := LoadPolicy([]byte(raw))
		if err != nil || p.PreflightTimeoutSeconds < 60 {
			t.Fatal(p, err)
		}
	}
	for _, raw := range []string{`{"preflight_timeout_seconds":-1}`, `{"preflight_timeout_seconds":86401}`} {
		if _, err := LoadPolicy([]byte(raw)); err == nil {
			t.Fatal("invalid timeout accepted", raw)
		}
	}
}
