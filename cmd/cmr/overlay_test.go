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

func TestCLIOverlayPolicyFlagPrecedenceAndReplay(t *testing.T) {
	for _, command := range []string{"recommend", "spawn"} {
		for _, flagOverride := range []bool{false, true} {
			root := cliEnvironment(t)
			candidates := candidateFile(t, root, false)
			overlay := filepath.Join(root, "fictional-overlay.json")
			raw, err := os.ReadFile("../../pkg/recommend/testdata/fictional-overlay.json")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(overlay, raw, 0600); err != nil {
				t.Fatal(err)
			}
			policy := filepath.Join(root, "policy.toml")
			selectedPath := overlay
			if flagOverride {
				selectedPath = filepath.Join(root, "missing-overlay.json")
			}
			if err = os.WriteFile(policy, []byte("catalog_overlay = '"+selectedPath+"'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{command, "--role", "developer", "--task-class", "code.implement", "--difficulty", "critical", "--policy", policy, "--candidates", candidates, "--host", "fictional-host", "--json"}
			if flagOverride {
				args = append(args, "--catalog-overlay", overlay)
			}
			log := ""
			if command == "spawn" {
				log = fakeTaskBoard(t, root)
				args = append(args, "--", "TASK", "--background")
			}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			paths, err := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
			if err != nil || len(paths) != 1 {
				t.Fatal(paths, err)
			}
			saved, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			// Removing the operator file cannot affect replay.
			if err = os.Remove(overlay); err != nil {
				t.Fatal(err)
			}
			d, err := recommend.LoadDecision(saved)
			if err != nil {
				t.Fatal(err)
			}
			if d.Inputs.CatalogOverlay == nil || d.Inputs.OverlayDigest == "" || d.Recommendation.Selected == nil || d.Recommendation.Selected.Model != "gpt-6-luna" || d.Recommendation.Selected.Effort != "low" {
				t.Fatal(d)
			}
			if command == "recommend" {
				var result recommendOutput
				if err = json.Unmarshal(out.Bytes(), &result); err != nil || *result.Selected != *d.Recommendation.Selected {
					t.Fatal(err)
				}
			} else {
				argv, err := os.ReadFile(log)
				if err != nil || strings.Contains(string(argv), "catalog-overlay") || !strings.Contains(string(argv), "gpt-6-luna") {
					t.Fatal(string(argv), err)
				}
			}
		}
	}
}
func TestCLIMissingOverlayRefusesBeforeDiscovery(t *testing.T) {
	root := cliEnvironment(t)
	log := fakeTaskBoard(t, root)
	var out, stderr bytes.Buffer
	code := run([]string{"recommend", "--role", "developer", "--task-class", "code.implement", "--catalog-overlay", filepath.Join(root, "missing.json"), "--json"}, &out, &stderr)
	if code != 2 || !strings.Contains(out.String(), recommend.InvalidOverlay) {
		t.Fatal(code, out.String(), stderr.String())
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("discovery/launch attempted", err)
	}
}
