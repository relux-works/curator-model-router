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

func cliEnvironment(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("PATH", root)
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "MUSE_HOME", "MUSE_CONFIG_DIR", "MUSE_AUTH_PATH", "MUSE_SESSIONS_DIR", "GEMINI_CLI_HOME", "CODEX_MANAGED_PACKAGE_ROOT"} {
		t.Setenv(key, "")
	}
	return root
}
func candidateFile(t *testing.T, root string, empty bool) string {
	t.Helper()
	catalog, err := recommend.LoadCatalog(recommend.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	candidates := []recommend.Candidate{}
	if !empty {
		for _, r := range catalog.Rows {
			candidates = append(candidates, r.Candidate)
		}
	}
	b, _ := json.Marshal(candidates)
	path := filepath.Join(root, "candidates.json")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestRecommendRecordsAndRules(t *testing.T) {
	root := cliEnvironment(t)
	file := candidateFile(t, root, false)
	args := []string{"recommend", "--role", "developer", "--task-class", "code.implement", "--candidates", file, "--host", "<your-build-host>", "--story", "<STORY-ID>", "--producer-family", "Anthropic", "--exclude", "claude/claude-sonnet-4-6", "--json", "--policy", "../../docs/standing-rules.toml"}
	var out, stderr bytes.Buffer
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	var result recommendOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Selected == nil || result.Selected.Model != "gpt-6.1-sol" || result.Selected.Effort != "high" || len(result.AppliedRules) == 0 {
		t.Fatal(result)
	}
	if len(result.Flags) != 6 || result.AdmissionSource != "candidates-file" {
		t.Fatal(result)
	}
	paths, err := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	b, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var record recommend.DecisionRecord
	if err = json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	if err = record.VerifyContentID(); err != nil {
		t.Fatal(err)
	}
	if err = recommend.Replay(record); err != nil {
		t.Fatal(err)
	}
	if record.Inputs.Host != "<your-build-host>" || record.Inputs.Story != "<STORY-ID>" || len(record.Inputs.Exclude) != 1 || record.Inputs.ProducerFamily != "Anthropic" {
		t.Fatal(record.Inputs)
	}
	// Human output includes rule source, ranks and exact flags.
	args = args[:len(args)-2]
	args = append(args, "--policy", "../../docs/standing-rules.toml")
	for i, a := range args {
		if a == "--json" {
			args = append(args[:i], args[i+1:]...)
			break
		}
	}
	out.Reset()
	stderr.Reset()
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	for _, want := range []string{"source=operator ruling: host reviewer pin (example)", "flags:", "decision=sha256:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
}
func TestHumanRankedAlternatives(t *testing.T) {
	root := cliEnvironment(t)
	file := candidateFile(t, root, false)
	var out, stderr bytes.Buffer
	if code := run([]string{"recommend", "--role", "developer", "--task-class", "code.implement", "--candidates", file}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "alternative=1 ") {
		t.Fatal(code, out.String(), stderr.String())
	}
}

func TestRecommendRefusalAndUsageShow(t *testing.T) {
	root := cliEnvironment(t)
	empty := candidateFile(t, root, true)
	var out, stderr bytes.Buffer
	if code := run([]string{"recommend", "--role", "developer", "--task-class", "code.implement", "--candidates", empty, "--json"}, &out, &stderr); code != 2 || !strings.Contains(out.String(), "no_qualified_candidate") {
		t.Fatal(code, out.String(), stderr.String())
	}
	out.Reset()
	if code := run([]string{"usage", "show", "--json"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), `"records":[]`) {
		t.Fatal(code, out.String(), stderr.String())
	}
	for _, args := range [][]string{{"recommend", "--json"}, {"usage", "refresh", "--ttl", "0s", "--json"}, {"usage", "refresh", "--runtime", "invalid", "--json"}, {"recommend", "--wat", "--json"}} {
		out.Reset()
		stderr.Reset()
		if code := run(args, &out, &stderr); code != 2 || !strings.Contains(out.String(), `"error"`) {
			t.Fatal(args, code, out.String(), stderr.String())
		}
	}
}

// Fan-out recommendations print per-family launch flags; each must request a
// parallel run, or task-board returns the existing live run for later launches.
func TestRecommendFanoutFlagsAllowParallel(t *testing.T) {
	root := cliEnvironment(t)
	file := candidateFile(t, root, false)
	var out, stderr bytes.Buffer
	args := []string{"recommend", "--role", "researcher", "--task-class", "research", "--candidates", file, "--fanout", "--json"}
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	var got struct {
		FanoutFlags [][]string `json:"fanout_flags"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.FanoutFlags) < 2 {
		t.Fatal(err, out.String())
	}
	for _, flags := range got.FanoutFlags {
		if flags[len(flags)-1] != "--allow-parallel" {
			t.Fatal(flags)
		}
	}
	out.Reset()
	args = args[:len(args)-1]
	if code := run(args, &out, &stderr); code != 0 || strings.Count(out.String(), "--allow-parallel") != len(got.FanoutFlags) {
		t.Fatal(code, out.String())
	}
}
