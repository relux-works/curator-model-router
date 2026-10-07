package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestSpawnWrapperLiteralValues(t *testing.T) {
	for _, story := range []string{"--", "--json", "--mode=shadow"} {
		t.Run(story, func(t *testing.T) {
			root := cliEnvironment(t)
			file := candidateFile(t, root, false)
			log := fakeTaskBoard(t, root)
			forwarded := []string{"TASK", "--background", "--instruction", "--", "--", "literal"}
			args := []string{"spawn", "--role", "developer", "--task-class", "code.implement", "--candidates", file, "--story", story, "--mode", "recommend", "--json", "--"}
			var out, stderr bytes.Buffer
			if code := run(append(args, forwarded...), &out, &stderr); code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			var result recommendOutput
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			got := result.Commands[0]
			if !reflect.DeepEqual(got[2:6], forwarded[:4]) || !reflect.DeepEqual(got[len(got)-2:], forwarded[4:]) {
				t.Fatal(got)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("recommend launched")
			}
		})
	}
}

func TestImporterDispatchConsumesValues(t *testing.T) {
	for _, tc := range []struct {
		args []string
		eval bool
	}{
		{[]string{"import", "input.json", "--store", "--importer=evalrun"}, false},
		{[]string{"import", "input.json", "--store", "--", "--importer", "evalrun"}, true},
		{[]string{"import", "input.json", "--registry", "--importer=evalrun"}, false},
		{[]string{"import", "input.json", "--store=--", "--importer=evalrun"}, true},
		{[]string{"import", "input.json", "--", "--importer=evalrun"}, false},
		{[]string{"import", "input.json", "--importer", "native", "--store", "--importer=evalrun"}, false},
	} {
		if got := isEvalRunCommand(tc.args); got != tc.eval {
			t.Fatalf("%q: got %t", tc.args, got)
		}
	}
	// The same parser must preserve a literal terminator as a value and as a
	// positional, without reinterpreting a directory as a dispatch flag.
	f := flag.NewFlagSet("test", flag.ContinueOnError)
	store := f.String("store", "", "")
	importer := f.String("importer", "native", "")
	if err := parseEvidenceFlags(f, []string{"input.json", "--store", "--", "--importer", "evalrun", "--", "--json"}); err != nil || *store != "--" || *importer != "evalrun" || !reflect.DeepEqual(f.Args(), []string{"input.json", "--json"}) {
		t.Fatal(store, importer, f.Args(), err)
	}
}

func TestHandScannersConsumeOptionValues(t *testing.T) {
	for _, value := range []string{"--", "--json", "--input=literal"} {
		got, err := routeFlags([]string{"--input", value}, "--input")
		if err != nil || got["--input"] != value || hasJSON([]string{"--input", value}) {
			t.Fatal(value, got, err)
		}
	}
	for _, name := range []string{"story", "store", "statement", "file", "reason", "mapping", "runtime"} {
		if hasJSON([]string{"--" + name, "--json"}) {
			t.Fatal("value changed output mode", name)
		}
		if !hasJSON([]string{"--" + name, "--", "--json"}) {
			t.Fatal("literal value hid json flag", name)
		}
	}
	if hasJSON([]string{"--", "--json"}) || hasJSON([]string{"--json=false"}) {
		t.Fatal("terminator/false ignored")
	}
	f := flag.NewFlagSet("positions", flag.ContinueOnError)
	f.Bool("json", false, "")
	if err := parseEvidenceFlags(f, []string{"--", "--json"}); err != nil || !reflect.DeepEqual(f.Args(), []string{"--json"}) {
		t.Fatal(f.Args(), err)
	}
}

func TestHumanFlagsFirstLines(t *testing.T) {
	for _, c := range []recommend.Candidate{{Runtime: "codex", Model: "gpt-6.1-sol", Effort: "high"}, {Runtime: "agy", Model: "gemini-3.8-flash-high", Effort: "none"}} {
		r := recommend.DecisionRecord{Recommendation: recommend.Recommendation{Selected: &c, DecisionID: "sha256:test"}}
		var out bytes.Buffer
		if err := outputRecommendation(r, false, nil, "", &out); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(out.String(), "\n")
		want := []string{"selected: " + c.Runtime + "/" + c.Model + "/" + c.Effort, "flags: " + strings.Join(spawnFlags(c), " ")}
		if !reflect.DeepEqual(lines[:2], want) || strings.Count(out.String(), "flags:") != 1 {
			t.Fatal(out.String())
		}
	}
}

func TestStandingRulePlaceholders(t *testing.T) {
	data, err := os.ReadFile("../../docs/standing-rules.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	policy, err := cmrio.DecodePolicy(data, false)
	if err != nil {
		t.Fatal(err)
	}
	assertPlaceholder := func(value string) {
		t.Helper()
		if len(value) < 3 || !strings.HasPrefix(value, "<") || !strings.HasSuffix(value, ">") {
			t.Fatal("template selector must be an angle-bracket placeholder", value)
		}
	}
	if policy.Host != "" {
		assertPlaceholder(policy.Host)
	}
	for _, rule := range policy.Rules {
		if rule.When.Host != "" {
			assertPlaceholder(rule.When.Host)
		}
		if rule.When.Story != "" {
			assertPlaceholder(rule.When.Story)
		}
		if rule.CrossProviderReview != nil {
			for _, host := range rule.CrossProviderReview.ExceptHosts {
				assertPlaceholder(host)
			}
		}
	}
	for _, kept := range []string{`host = "<your-build-host>"`, `host = "<your-other-host>"`, `story = "<STORY-ID>"`, "<PROJECT-ID>", "operator ruling: host reviewer pin (example)", "operator ruling: quota stop (example)", "operator ruling: cross-provider review (example)", "operator ruling: refusal rotation (example)", "operator ruling: second host pairs (example)", "[rules.require]", "[rules.prefer]", "[rules.effort]"} {
		if !strings.Contains(text, kept) {
			t.Fatal("template lost ruling/example", kept)
		}
	}
	// Still a usable TOML policy after the public selectors were replaced.
	root := cliEnvironment(t)
	if _, err := recommendation(recommendOptions{role: "developer", taskClass: "code.implement", host: "<your-build-host>", story: "<STORY-ID>", policy: "../../docs/standing-rules.toml", candidates: candidateFile(t, root, false)}); err != nil {
		t.Fatal(err)
	}
}

func TestImporterLiteralDirectoryValuesReachHandler(t *testing.T) {
	for _, eval := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "evalrun"}[eval], func(t *testing.T) {
			root := cliEnvironment(t)
			native, err := filepath.Abs(evidenceFixture(t, "roundtrip.input.json"))
			if err != nil {
				t.Fatal(err)
			}
			registry, err := filepath.Abs(evidenceFixture(t, "registry.json"))
			if err != nil {
				t.Fatal(err)
			}
			export, err := filepath.Abs(evidenceFixture(t, "evalrun/export.json"))
			if err != nil {
				t.Fatal(err)
			}
			mapping, err := filepath.Abs(evidenceFixture(t, "evalrun/mapping.json"))
			if err != nil {
				t.Fatal(err)
			}
			evalRegistry, err := filepath.Abs(evidenceFixture(t, "evalrun/registry.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			args := []string{"evidence", "import", native, "--store", "--importer=evalrun", "--registry", registry, "--json"}
			if eval {
				args = []string{"evidence", "import", export, "--store", "--", "--importer", "evalrun", "--mapping", mapping, "--registry", evalRegistry, "--json"}
			}
			raw := callEvidenceCLI(t, args, 0)
			var imported struct {
				ImportDigest string `json:"import_digest"`
			}
			if err := json.Unmarshal(raw, &imported); err != nil || imported.ImportDigest == "" {
				t.Fatal("incorrect handler", string(raw), err)
			}
		})
	}
}
