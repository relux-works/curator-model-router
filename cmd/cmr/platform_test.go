package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestRecommendAndSpawnPlatformConstraints(t *testing.T) {
	for _, command := range []string{"recommend", "spawn"} {
		for _, override := range []string{"", "target-os"} {
			t.Run(command+"/"+override, func(t *testing.T) {
				root := cliEnvironment(t)
				log := fakeTaskBoard(t, root)
				platform := override
				if platform == "" {
					platform = runtime.GOOS
				}
				catalog, err := recommend.LoadCatalog(recommend.DefaultCatalog())
				if err != nil {
					t.Fatal(err)
				}
				var row recommend.CatalogRow
				for _, r := range catalog.Rows {
					if r.Model == "gpt-6.1-sol" && r.Effort == "high" {
						row = r
						break
					}
				}
				row.Constraints.NotFor = []string{platform}
				catalog.Rows = []recommend.CatalogRow{row}
				data, err := json.Marshal(catalog)
				if err != nil {
					t.Fatal(err)
				}
				catalogPath := filepath.Join(root, "catalog.json")
				if err = os.WriteFile(catalogPath, data, 0600); err != nil {
					t.Fatal(err)
				}
				data, err = json.Marshal([]recommend.Candidate{row.Candidate})
				if err != nil {
					t.Fatal(err)
				}
				candidatePath := filepath.Join(root, "candidates.json")
				if err = os.WriteFile(candidatePath, data, 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{command, "--role", "developer", "--task-class", "code.implement", "--catalog", catalogPath, "--candidates", candidatePath, "--json"}
				if override != "" {
					args = append(args, "--platform", override)
				}
				if command == "spawn" {
					args = append(args, "--", "TASK", "--background")
				}
				var out, stderr bytes.Buffer
				if code := run(args, &out, &stderr); code != 2 || !strings.Contains(out.String(), "no_qualified_candidate") || !strings.Contains(out.String(), "constraint_not_for") {
					t.Fatal(code, out.String(), stderr.String())
				}
				if _, err = os.Stat(log); !os.IsNotExist(err) {
					t.Fatal("platform refusal launched", err)
				}
				paths, err := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
				if err != nil || len(paths) != 1 {
					t.Fatal(paths, err)
				}
				data, err = os.ReadFile(paths[0])
				if err != nil {
					t.Fatal(err)
				}
				var record recommend.DecisionRecord
				if err = json.Unmarshal(data, &record); err != nil || record.Inputs.Task.Platform != platform {
					t.Fatal(record.Inputs.Task, err)
				}
			})
		}
	}
}
