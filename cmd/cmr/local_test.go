package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestLocalImportCLI(t *testing.T) {
	for _, tc := range []struct{ command, file, version string }{
		{"import-base", "fictional-public-base-export.json", recommend.BaseRecordsVersion},
		{"coefficients", "fictional-coefficients.json", recommend.CoefficientTableVersion},
	} {
		t.Run(tc.command, func(t *testing.T) {
			var out, errout bytes.Buffer
			code := run([]string{"local", tc.command, "--input", filepath.Join("..", "..", "pkg", "recommend", "testdata", tc.file)}, &out, &errout)
			if code != 0 {
				t.Fatal(code, errout.String())
			}
			var value struct {
				SchemaVersion string `json:"schema_version"`
			}
			if err := json.Unmarshal(out.Bytes(), &value); err != nil || value.SchemaVersion != tc.version {
				t.Fatal(out.String(), err)
			}
		})
	}
	var out, errout bytes.Buffer
	if run([]string{"local", "providers"}, &out, &errout) != 0 {
		t.Fatal(errout.String())
	}
	var infos []recommend.BaseProviderInfo
	if err := json.Unmarshal(out.Bytes(), &infos); err != nil || len(infos) != 1 || infos[0].NeedsKey {
		t.Fatal(out.String(), err)
	}
	for _, args := range [][]string{{"local"}, {"local", "import-base"}, {"local", "import-base", "--input", "missing.json"}, {"local", "coefficients", "--input", "missing.json"}, {"local", "providers", "--unexpected"}} {
		out.Reset()
		errout.Reset()
		if run(args, &out, &errout) != 2 {
			t.Fatal("invalid accepted", args)
		}
	}
}
func TestLocalDecideOfflineCLI(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "pkg", "recommend", "testdata", "fictional-local-capability.json"))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := recommend.FreezeLocalCapability(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := bundle.Document()
	if err != nil {
		t.Fatal(err)
	}
	c := recommend.Candidate{Runtime: "local", Model: "fictional", Effort: "none", WeightsID: doc.Materializations[0].WeightsID, ExpectedWeightsID: doc.Materializations[0].WeightsID, Reasoning: &recommend.ReasoningContext{Thinking: "on", Effort: "high"}}
	request := recommend.Request{SchemaVersion: recommend.LocalRequestVersion, LocalCapability: &bundle, Catalog: recommend.Catalog{SchemaVersion: recommend.CatalogVersion, Rows: []recommend.CatalogRow{{Candidate: c, Family: "fictional", Billing: "local", Cost: recommend.Cost{Kind: "estimate", Source: "FICTIONAL", AsOf: "2026-10-09"}}}}, Candidates: []recommend.Candidate{c}, Task: recommend.TaskProfile{Role: "developer", TaskClass: "code.implement", Difficulty: "hard"}, Policy: recommend.DefaultPolicy(), Usage: recommend.UsageSnapshot{AsOf: 1800000000}}
	raw, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if code := run([]string{"local", "decide", "--input", file}, &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
	d, err := recommend.LoadDecision(out.Bytes())
	if err != nil || d.Recommendation.Selected == nil {
		t.Fatal(out.String(), err)
	}
}
