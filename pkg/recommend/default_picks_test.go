package recommend

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

type defaultPick struct {
	Role         string    `json:"role"`
	TaskClass    string    `json:"task_class"`
	Difficulty   string    `json:"difficulty"`
	Budget       string    `json:"budget"`
	Candidate    Candidate `json:"candidate"`
	Tier         Tier      `json:"tier"`
	QualityIndex string    `json:"quality_index"`
	DecisionID   string    `json:"decision_id"`
}

func TestPublicDefaultPicksGoldenAndNotes(t *testing.T) {
	picks := []defaultPick{}
	table := "| Role / class | Difficulty | Economy | Balanced | Burn |\n| --- | --- | --- | --- | --- |\n"
	for _, role := range []struct{ role, class string }{{"developer", "code.implement"}, {"reviewer", "review.code"}, {"researcher", "research"}, {"orchestrator", "orchestration"}} {
		for _, difficulty := range []string{"trivial", "routine", "standard", "hard", "critical"} {
			table += fmt.Sprintf("| %s / %s | %s |", role.role, role.class, difficulty)
			for _, budget := range []string{"economy", "balanced", "burn"} {
				in := realRequest(t)
				in.Task = TaskProfile{Role: role.role, TaskClass: role.class, Difficulty: difficulty}
				in.Policy.BudgetMode = budget
				d, err := BuildDecision(in)
				if err != nil {
					t.Fatal(err)
				}
				x := chosenExplanation(t, d.Recommendation)
				picks = append(picks, defaultPick{role.role, role.class, difficulty, budget, x.Candidate, x.Tier, x.QualityIndex, d.DecisionID})
				table += fmt.Sprintf(" `%s/%s/%s` (%s) |", x.Candidate.Runtime, x.Candidate.Model, x.Candidate.Effort, x.Tier)
			}
			table += "\n"
		}
	}
	path := "testdata/default-picks.golden.json"
	notesPath := "../../catalog/NOTES.md"
	raw, err := os.ReadFile(notesPath)
	if err != nil {
		t.Fatal(err)
	}
	marker := "<!-- DEFAULT-PICKS -->\n"
	prefix, _, ok := strings.Cut(string(raw), marker)
	if !ok {
		t.Fatal("missing notes marker")
	}
	if os.Getenv("PUBED_UPDATE") == "1" {
		b, err := json.MarshalIndent(picks, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(notesPath, []byte(prefix+marker+table), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []defaultPick
	if err = json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, picks) {
		t.Fatal("default pick golden differs")
	}
	raw, err = os.ReadFile(notesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != prefix+marker+table {
		t.Fatal("notes default table differs")
	}
}
