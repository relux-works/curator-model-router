package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/evidence"
)

func evalCLIFlags(t *testing.T) []string {
	t.Helper()
	return []string{"--mapping", evidenceFixture(t, "evalrun/mapping.json"), "--registry", evidenceFixture(t, "evalrun/registry.json"), "--store", t.TempDir(), "--json"}
}
func TestEvalRunCLIImportAndAlias(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(map[bool]string{false: "ImportEvalRun", true: "ImportFlagAlias"}[alias], func(t *testing.T) {
			args := []string{"evidence", "import-evalrun", evidenceFixture(t, "evalrun/export.json")}
			if alias {
				args = []string{"evidence", "import", evidenceFixture(t, "evalrun/export.json"), "--importer", "evalrun"}
			}
			args = append(args, evalCLIFlags(t)...)
			var first, again evidence.ImportResult
			if err := json.Unmarshal(callEvidenceCLI(t, args, 0), &first); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(callEvidenceCLI(t, args, 0), &again); err != nil {
				t.Fatal(err)
			}
			if first.ImportDigest != again.ImportDigest || first.SnapshotDigest != again.SnapshotDigest || !again.AlreadyPresent || first.ImportedAt != "2026-10-02T00:00:00Z" {
				t.Fatal("CLI idempotence/time lost")
			}
			want, err := os.ReadFile(evidenceFixture(t, "evalrun/import.digest"))
			if err != nil || first.ImportDigest != strings.TrimSpace(string(want)) {
				t.Fatal("CLI changed import")
			}
		})
	}
}
func TestEvalRunCLIArgumentAndJSONRefusalVectors(t *testing.T) {
	for _, v := range []struct {
		name string
		args []string
		code string
	}{
		{"EvalRunImportedAt", []string{"evidence", "import-evalrun", evidenceFixture(t, "evalrun/export.json"), "--imported-at", "2026-10-02T00:00:00Z"}, "cmr_invalid_arguments"},
		{"EvalRunEmptyImportedAt", []string{"evidence", "import-evalrun", evidenceFixture(t, "evalrun/export.json"), "--imported-at="}, "cmr_invalid_arguments"},
		{"EvalRunAliasImportedAt", []string{"evidence", "import", evidenceFixture(t, "evalrun/export.json"), "--importer=evalrun", "--imported-at", "2026-10-02T00:00:00Z"}, "cmr_invalid_arguments"},
		{"OtherImporterMapping", []string{"evidence", "import", evidenceFixture(t, "evalrun/import.canonical.json"), "--importer", "native"}, "cmr_invalid_arguments"},
		{"BugHuntMapping", []string{"evidence", "import", evidenceFixture(t, "evalrun/export.json"), "--importer", "bughunt"}, "cmr_invalid_arguments"},
		{"JSONCLIRefusal", []string{"evidence", "import-evalrun", evidenceFixture(t, "evalrun/null.json")}, "canonical_null"},
		{"MissingExport", []string{"evidence", "import-evalrun"}, "cmr_invalid_arguments"},
	} {
		t.Run(v.name, func(t *testing.T) {
			raw := callEvidenceCLI(t, append(v.args, evalCLIFlags(t)...), 2)
			if !strings.Contains(string(raw), `"code":"`+v.code+`"`) {
				t.Fatalf("refusal=%s", raw)
			}
		})
	}
	t.Run("MissingMapping", func(t *testing.T) {
		callEvidenceCLI(t, []string{"evidence", "import-evalrun", evidenceFixture(t, "evalrun/export.json"), "--store", t.TempDir(), "--json"}, 2)
	})
}
func TestEvalRunCLIRuntimeDiagnosticsAndExplicitDerivation(t *testing.T) {
	raw, err := os.ReadFile(evidenceFixture(t, "evalrun/export.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v evidence.EvalRun
	if err = evidence.Decode(raw, &v); err != nil {
		t.Fatal(err)
	}
	for i := range v.Results {
		v.Results[i].Subject.RuntimeName = "unmapped-runtime"
	}
	raw, err = json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "export.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	flags := evalCLIFlags(t)
	out := callEvidenceCLI(t, append([]string{"evidence", "import-evalrun", path}, flags...), 0)
	var result evidence.ImportResult
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	found := false
	seen := map[evidence.Issue]bool{}
	for _, i := range result.Report.Issues {
		if seen[i] {
			t.Fatal("duplicate report issue")
		}
		seen[i] = true
		found = found || i.Code == "runtime_unresolved"
	}
	if !found {
		t.Fatal("runtime diagnostics lost")
	}
	root := t.TempDir()
	storeFlags := []string{"--store", root, "--registry", evidenceFixture(t, "evalrun/registry.json"), "--json"}
	callEvidenceCLI(t, append([]string{"evidence", "import-evalrun", evidenceFixture(t, "evalrun/export.json"), "--mapping", evidenceFixture(t, "evalrun/mapping.json")}, storeFlags...), 0)
	args := append([]string{"suitability", "--role", "reviewer", "--candidates", evidenceFixture(t, "candidate.json"), "--evaluated-at", "2026-10-02T00:00:00Z"}, storeFlags...)
	var view evidence.View
	if err = json.Unmarshal(callEvidenceCLI(t, args, 0), &view); err != nil {
		t.Fatal(err)
	}
	if view.Derivation.Version != "1" || view.Results[0].Score != nil {
		t.Fatal("default derivation changed")
	}
	if err = json.Unmarshal(callEvidenceCLI(t, append(args, "--derivation", evidenceFixture(t, "evalrun/review-derivation-v2.json")), 0), &view); err != nil {
		t.Fatal(err)
	}
	if view.Derivation.Version != "2" || view.Results[0].Score == nil || math.Abs(*view.Results[0].Score-0.8) > 1e-12 {
		t.Fatal("explicit v2 not used")
	}
	// JSON refusals must be stdout-only; plain refusals remain stderr-only.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"evidence", "import-evalrun"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatal("plain refusal channels changed")
	}
}

func TestEvalRunCLIDerivationStrictRefusals(t *testing.T) {
	for _, v := range []struct{ name, path, code string }{{"MissingFile", "missing-derivation.json", "cmr_invalid_arguments"}, {"UnknownField", evidenceFixture(t, "evalrun/unknown-field.json"), "evidence_unknown_field"}} {
		t.Run(v.name, func(t *testing.T) {
			args := []string{"suitability", "--role", "reviewer", "--derivation", v.path, "--store", t.TempDir(), "--json"}
			out := callEvidenceCLI(t, args, 2)
			if !strings.Contains(string(out), `"code":"`+v.code+`"`) {
				t.Fatalf("refusal=%s", out)
			}
		})
	}
}
