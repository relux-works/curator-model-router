package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/relux-works/curator-model-router/pkg/evidence"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func callEvidenceCLI(t *testing.T, args []string, want int) []byte {
	t.Helper()
	var out, stderr bytes.Buffer
	code := run(args, &out, &stderr)
	if code != want {
		t.Fatalf("run %v: code=%d out=%s stderr=%s", args, code, out.String(), stderr.String())
	}
	if hasJSON(args) && stderr.Len() != 0 {
		t.Fatalf("JSON refusal went to stderr: %s", stderr.String())
	}
	return out.Bytes()
}
func evidenceFixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "pkg", "evidence", "testdata", name)
}
func cliRootFlags(t *testing.T) []string {
	t.Helper()
	return []string{"--store", t.TempDir(), "--registry", evidenceFixture(t, "registry.json"), "--json"}
}
func TestEvidenceCLIImportInspectUnresolved(t *testing.T) {
	flags := cliRootFlags(t)
	args := append([]string{"evidence", "import", evidenceFixture(t, "roundtrip.input.json")}, flags...)
	raw := callEvidenceCLI(t, args, 0)
	var imported evidence.ImportResult
	if err := json.Unmarshal(raw, &imported); err != nil {
		t.Fatal(err)
	}
	if imported.ImportDigest == "" || imported.SnapshotDigest == "" {
		t.Fatal("missing import ids")
	}
	raw = callEvidenceCLI(t, append([]string{"evidence", "ls"}, flags...), 0)
	var listed struct {
		Active evidence.ActiveSet `json:"active_set"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Active.Records) != 3 {
		t.Fatalf("got %d records", len(listed.Active.Records))
	}
	for _, rec := range listed.Active.Records {
		raw = callEvidenceCLI(t, append([]string{"evidence", "show", rec.ID}, flags...), 0)
		var shown evidence.Record
		if err := json.Unmarshal(raw, &shown); err != nil {
			t.Fatal(err)
		}
		if shown.Status != rec.Status {
			t.Fatal("show lost derived status")
		}
	}
	raw = callEvidenceCLI(t, append([]string{"evidence", "unresolved"}, flags...), 0)
	if !strings.Contains(string(raw), "\"issues\":[]") {
		t.Fatalf("unexpected unresolved output: %s", raw)
	}
}
func TestNotesCLIAppendAndRetract(t *testing.T) {
	flags := cliRootFlags(t)
	raw := callEvidenceCLI(t, append([]string{"note", "add", "--file", evidenceFixture(t, "note.json"), "--at", "2026-10-01T00:00:00Z"}, flags...), 0)
	if !strings.Contains(string(raw), "import_digest") {
		t.Fatal("note add did not create import")
	}
	raw = callEvidenceCLI(t, append([]string{"evidence", "ls"}, flags...), 0)
	var listed struct {
		Active evidence.ActiveSet `json:"active_set"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	id := listed.Active.Records[0].ID
	callEvidenceCLI(t, append([]string{"note", "retract", id, "--reason", "withdrawn", "--author", "operator", "--at", "2026-10-02T00:00:00Z"}, flags...), 0)
	raw = callEvidenceCLI(t, append([]string{"evidence", "show", id}, flags...), 0)
	var shown evidence.Record
	if err := json.Unmarshal(raw, &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Status != "retracted" {
		t.Fatal("note was not withdrawn")
	}
	callEvidenceCLI(t, append([]string{"note", "add", "--model", "gpt-6.1-sol", "--efforts", "high", "--categories", "review.code", "--statement", "Review prior", "--author", "operator", "--review-by", "2026-11-01", "--created-at", "2026-10-01T00:00:00Z", "--at", "2026-10-01T00:00:00Z"}, flags...), 0)
}
func TestSuitabilityCLIFrozenTimeAndProject(t *testing.T) {
	flags := cliRootFlags(t)
	callEvidenceCLI(t, append([]string{"note", "add", "--file", evidenceFixture(t, "note.json"), "--at", "2026-10-01T00:00:00Z"}, flags...), 0)
	args := append([]string{"suitability", "--role", "reviewer", "--candidates", evidenceFixture(t, "candidate.json"), "--evaluated-at", "2026-10-01T00:00:00Z"}, flags...)
	first := callEvidenceCLI(t, args, 0)
	again := callEvidenceCLI(t, args, 0)
	if !bytes.Equal(first, again) {
		t.Fatal("same frozen CLI inputs changed view")
	}
	var view evidence.View
	if err := json.Unmarshal(first, &view); err != nil {
		t.Fatal(err)
	}
	if view.Inputs.EvaluatedAt != "2026-10-01T00:00:00Z" {
		t.Fatal("evaluation time not printed")
	}
	if len(view.Results) != 1 {
		t.Fatal("missing candidate result")
	}
	var vectors struct {
		Vectors []struct {
			Requirements evidence.RequirementsInput `json:"requirements"`
		} `json:"vectors"`
	}
	b, err := os.ReadFile(evidenceFixture(t, "derivation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	req := vectors.Vectors[3].Requirements
	b, err = json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "requirements.json")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	changed := callEvidenceCLI(t, append(args, "--project-reqs", path), 0)
	if bytes.Equal(changed, first) {
		t.Fatal("project requirements reused view")
	}
	// Only cmd defaults a missing time, and it prints the chosen value.
	raw := callEvidenceCLI(t, append([]string{"suitability", "--role", "reviewer", "--candidates", evidenceFixture(t, "candidate.json")}, flags...), 0)
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Inputs.EvaluatedAt == "" {
		t.Fatal("default evaluation time not printed")
	}
}
func TestEvidenceCLIJSONRefusals(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code string
	}{
		{"no-action", []string{"evidence"}, "cmr_invalid_arguments"},
		{"unknown-action", []string{"evidence", "bad"}, "cmr_unknown_subcommand"},
		{"unknown-flag", []string{"evidence", "ls", "--bad"}, "cmr_invalid_arguments"},
		{"missing-file", []string{"evidence", "import"}, "cmr_invalid_arguments"},
		{"missing-note", []string{"note"}, "cmr_invalid_arguments"},
		{"missing-role", []string{"suitability"}, "cmr_invalid_arguments"},
		{"invalid-time", []string{"suitability", "--role", "reviewer", "--evaluated-at", "invalid"}, "evidence_invalid_time"},
		{"unrecognised-role", []string{"suitability", "--role", "undefined-role"}, "evidence_unknown_role"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append(tc.args, cliRootFlags(t)...)
			if tc.name == "no-action" {
				args = []string{"evidence", "--json", "--store", t.TempDir()}
			}
			if tc.name == "missing-note" {
				args = []string{"note", "--json", "--store", t.TempDir()}
			}
			raw := callEvidenceCLI(t, args, 2)
			if !strings.Contains(string(raw), "\"code\":\""+tc.code+"\"") {
				t.Fatalf("unexpected refusal: %s", raw)
			}
		})
	}
}
func TestEvidenceCLIPlainAndOutputFailure(t *testing.T) {
	for _, command := range []string{"evidence", "note", "suitability"} {
		t.Run(command, func(t *testing.T) {
			var out, stderr bytes.Buffer
			args := []string{command, "--invalid", "--store", t.TempDir()}
			if code := run(args, &out, &stderr); code != 2 || out.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("plain refusal code=%d out=%s err=%s", code, out.String(), stderr.String())
			}
		})
	}
	flags := cliRootFlags(t)
	var out failFirstWriter
	var stderr bytes.Buffer
	if code := run(append([]string{"evidence", "ls"}, flags...), &out, &stderr); code != 2 || !strings.Contains(out.String(), "cmr_output_failed") || stderr.Len() != 0 {
		t.Fatalf("output refusal code=%d out=%s err=%s", code, out.String(), stderr.String())
	}
}

func TestBugHuntCLIImport(t *testing.T) {
	flags := []string{"--store", t.TempDir(), "--json"}
	raw := callEvidenceCLI(t, append([]string{"evidence", "import", filepath.Join("..", "..", "pkg", "evidence", "data", "bughunt-export.json"), "--importer", "bughunt", "--imported-at", "2026-10-01T00:00:00Z"}, flags...), 0)
	var result evidence.ImportResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.SnapshotDigest == "" {
		t.Fatal("missing Bug Hunt snapshot")
	}
	raw = callEvidenceCLI(t, append([]string{"evidence", "ls"}, flags...), 0)
	var listed struct {
		Active evidence.ActiveSet `json:"active_set"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	export, err := evidence.PinnedExport()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Active.Records) != len(export.Rows) {
		t.Fatal("Bug Hunt CLI dropped a row")
	}
	costs := 0
	for _, rec := range listed.Active.Records {
		if rec.Observation.Metric == "list_cost" {
			costs++
			if rec.Observation.Cost == nil || rec.Observation.Cost.USD == nil || *rec.Observation.Cost.USD != 1.8 {
				t.Fatal("CLI lost reported cost")
			}
		}
	}
	if costs != 1 {
		t.Fatal("CLI lost cost row")
	}
}

func TestDefaultTimesPrintedPlainAndJSON(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			root := t.TempDir()
			flags := []string{"--store", root, "--registry", evidenceFixture(t, "registry.json")}
			if asJSON {
				flags = append(flags, "--json")
			}
			started := time.Now().UTC()
			raw := callEvidenceCLI(t, append([]string{"note", "add", "--model", "gpt-6.1-sol", "--efforts", "high", "--categories", "review.code", "--statement", "Current prior", "--author", "operator", "--review-by", "2030-01-01"}, flags...), 0)
			var added struct {
				evidence.ImportResult
				CreatedAt string `json:"created_at"`
				At        string `json:"at"`
			}
			if err := json.Unmarshal(raw, &added); err != nil {
				t.Fatal(err)
			}
			used, err := time.Parse(time.RFC3339Nano, added.ImportedAt)
			if err != nil || used.Before(started) || used.After(time.Now().UTC()) || added.CreatedAt != added.ImportedAt || added.At != added.ImportedAt {
				t.Fatalf("default time not printed: %s %v", raw, err)
			}
			var registry evidence.Registry
			b, err := os.ReadFile(evidenceFixture(t, "registry.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = evidence.Decode(b, &registry); err != nil {
				t.Fatal(err)
			}
			store, err := evidence.NewStore(root, registry)
			if err != nil {
				t.Fatal(err)
			}
			_, _, docs, active, err := store.Inspect()
			if err != nil || docs[0].ImportedAt != added.ImportedAt || docs[0].Notes[0].CreatedAt != added.CreatedAt {
				t.Fatal("printed time differs from stored note")
			}
			raw = callEvidenceCLI(t, append([]string{"note", "retract", active.Records[0].ID, "--reason", "withdrawn", "--author", "operator"}, flags...), 0)
			var retracted struct {
				evidence.ImportResult
				At string `json:"at"`
			}
			if err = json.Unmarshal(raw, &retracted); err != nil {
				t.Fatal(err)
			}
			used, err = time.Parse(time.RFC3339Nano, retracted.At)
			if err != nil || used.Before(started) || used.After(time.Now().UTC()) || retracted.At != retracted.ImportedAt {
				t.Fatalf("retraction time not printed: %s", raw)
			}
			_, _, docs, _, err = store.Inspect()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, doc := range docs {
				if len(doc.Retractions) == 1 {
					found = true
					if doc.ImportedAt != retracted.ImportedAt || doc.Retractions[0].At != retracted.At {
						t.Fatal("printed time differs from stored retraction")
					}
				}
			}
			if !found {
				t.Fatal("missing stored retraction")
			}
			raw = callEvidenceCLI(t, append([]string{"suitability", "--role", "reviewer", "--candidates", evidenceFixture(t, "candidate.json")}, flags...), 0)
			var view evidence.View
			if err = json.Unmarshal(raw, &view); err != nil {
				t.Fatal(err)
			}
			used, err = time.Parse(time.RFC3339Nano, view.Inputs.EvaluatedAt)
			if err != nil || used.Before(started) || used.After(time.Now().UTC()) {
				t.Fatalf("evaluation time not printed: %s", raw)
			}
		})
	}
}
func TestBugHuntCLIPinnedTimeReplay(t *testing.T) {
	export, err := evidence.PinnedExport()
	if err != nil {
		t.Fatal(err)
	}
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			flags := []string{"--store", t.TempDir()}
			if asJSON {
				flags = append(flags, "--json")
			}
			args := append([]string{"evidence", "import", filepath.Join("..", "..", "pkg", "evidence", "data", "bughunt-export.json"), "--importer", "bughunt"}, flags...)
			var first, second struct {
				evidence.ImportResult
				RetrievedAt string `json:"retrieved_at"`
			}
			if err := json.Unmarshal(callEvidenceCLI(t, args, 0), &first); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(callEvidenceCLI(t, args, 0), &second); err != nil {
				t.Fatal(err)
			}
			if first.RetrievedAt != export.RetrievedAt || second.RetrievedAt != export.RetrievedAt || first.ImportedAt != export.ImportedAt || second.ImportedAt != export.ImportedAt || first.ImportDigest != second.ImportDigest || first.SnapshotDigest != second.SnapshotDigest || !second.AlreadyPresent || second.Status != "already present" {
				t.Fatalf("non-idempotent CLI imports: %+v %+v", first, second)
			}
			var override evidence.ImportResult
			if err := json.Unmarshal(callEvidenceCLI(t, append(args, "--imported-at", "2026-10-02T00:00:00Z"), 0), &override); err != nil {
				t.Fatal(err)
			}
			if override.ImportedAt != "2026-10-02T00:00:00Z" || override.ImportDigest == first.ImportDigest {
				t.Fatal("explicit override ignored")
			}
			raw := callEvidenceCLI(t, append([]string{"evidence", "ls"}, flags...), 0)
			var listed struct {
				Active evidence.ActiveSet `json:"active_set"`
			}
			if err := json.Unmarshal(raw, &listed); err != nil {
				t.Fatal(err)
			}
			if len(listed.Active.Records) != len(export.Rows) {
				t.Fatal("repeat import duplicated observation IDs")
			}
			for _, rec := range listed.Active.Records {
				if rec.Observation.Provenance.RetrievedAt != export.RetrievedAt {
					t.Fatal("retrieval time not pinned")
				}
			}
		})
	}
}
func TestEvidenceCLIRebind(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			root := t.TempDir()
			registryFile := filepath.Join(t.TempDir(), "registry-a.json")
			registryA := evidence.Registry{SchemaVersion: evidence.SchemaVersion, Reference: evidence.Identity{Name: "fixture", Version: "A"}, Models: []evidence.RegistryModel{}}
			b, err := json.Marshal(registryA)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(registryFile, b, 0600); err != nil {
				t.Fatal(err)
			}
			flags := []string{"--store", root}
			if asJSON {
				flags = append(flags, "--json")
			}
			callEvidenceCLI(t, append([]string{"evidence", "import", evidenceFixture(t, "roundtrip.input.json"), "--registry", registryFile}, flags...), 0)
			raw := callEvidenceCLI(t, append([]string{"evidence", "unresolved"}, flags...), 0)
			var report evidence.Report
			if err = json.Unmarshal(raw, &report); err != nil || len(report.Issues) == 0 {
				t.Fatalf("registry A should leave unresolved: %s %v", raw, err)
			}
			callEvidenceCLI(t, append([]string{"evidence", "rebind"}, flags...), 2)
			raw = callEvidenceCLI(t, append([]string{"evidence", "rebind", "--registry", evidenceFixture(t, "registry.json")}, flags...), 0)
			var snap evidence.Snapshot
			if err = json.Unmarshal(raw, &snap); err != nil {
				t.Fatal(err)
			}
			if snap.RegistryRef == registryA.Reference {
				t.Fatal("CLI did not rebind")
			}
			raw = callEvidenceCLI(t, append([]string{"evidence", "unresolved"}, flags...), 0)
			if err = json.Unmarshal(raw, &report); err != nil || len(report.Issues) != 0 {
				t.Fatalf("registry B should resolve: %s %v", raw, err)
			}
			// No --registry: suitability must load B from the snapshot instead of using
			// the CLI's default Bug Hunt registry, which has a different digest.
			callEvidenceCLI(t, append([]string{"suitability", "--role", "reviewer", "--candidates", evidenceFixture(t, "candidate.json"), "--evaluated-at", "2026-10-01T00:00:00Z"}, flags...), 0)
		})
	}
}
