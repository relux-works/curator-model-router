package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func putCLIConfig(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := cmrio.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
}

// Inspection reports every candidate and effective layer, including explicit
// overrides and HOME defaults, without creating state/data or validating policy.
func TestConfigShowLayersAndReadOnly(t *testing.T) {
	for _, layer := range []string{"flag", "user", "system", "legacy", "default"} {
		t.Run(layer, func(t *testing.T) {
			root := cliEnvironment(t)
			policy := filepath.Join(root, "explicit.toml")
			catalog := filepath.Join(root, "explicit.json")
			policies := []string{policy, filepath.Join(cmrio.ConfigRoot(), "routing.toml"), filepath.Join(cmrio.SystemConfigRoot, "routing.toml"), cmrio.LegacyConfigPath("policy")}
			catalogs := []string{catalog, filepath.Join(cmrio.ConfigRoot(), "catalog.json"), filepath.Join(cmrio.SystemConfigRoot, "catalog.json"), cmrio.LegacyConfigPath("catalog")}
			index := map[string]int{"flag": 0, "user": 1, "system": 2, "legacy": 3, "default": 4}[layer]
			for i := index; i < 4; i++ {
				putCLIConfig(t, policies[i], []byte("malformed ["))
				putCLIConfig(t, catalogs[i], []byte("malformed ["))
			}
			args := []string{"config", "show", "--json"}
			if layer == "flag" {
				args = append(args, "--policy", policy, "--catalog", catalog)
			}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 0 || stderr.Len() != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			var got map[string]cmrio.ConfigSource
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"policy", "catalog"} {
				source := got[kind]
				if source.Layer != layer || len(source.Candidates) != 3+map[bool]int{true: 1}[layer == "flag"] {
					t.Fatal(kind, source)
				}
				if index < 4 {
					want := policies[index]
					if kind == "catalog" {
						want = catalogs[index]
					}
					if source.Path != want {
						t.Fatal(source)
					}
				} else if source.Path != "" {
					t.Fatal(source)
				}
				for _, candidate := range source.Candidates {
					_, err := os.Lstat(candidate.Path)
					if candidate.Exists != (err == nil) {
						t.Fatal(candidate, err)
					}
				}
			}
			for kind, path := range map[string]string{"state_root": cmrio.StateRoot(), "data_root": cmrio.DataRoot()} {
				if got[kind].Path != path || got[kind].Layer != "user" || got[kind].Candidates[0].Exists {
					t.Fatal(kind, got[kind])
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("show created root", path, err)
				}
			}
			out.Reset()
			args = append(args, "--json=false")
			if code := run(args, &out, &stderr); code != 0 || !strings.Contains(out.String(), "missing:") || !strings.Contains(out.String(), "("+layer+")") {
				t.Fatal(code, out.String(), stderr.String())
			}
		})
	}
}

// Recommend emits exactly one deprecation line for each legacy file actually
// loaded; a valid user file suppresses its corresponding legacy notice.
func TestRecommendLegacyNotices(t *testing.T) {
	for _, kind := range []string{"policy", "catalog", "both"} {
		t.Run(kind, func(t *testing.T) {
			root := cliEnvironment(t)
			candidates := candidateFile(t, root, false)
			for _, k := range []string{"policy", "catalog"} {
				name := "routing.toml"
				raw := []byte("")
				if k == "catalog" {
					name = "catalog.json"
					raw = recommend.DefaultCatalog()
				}
				putCLIConfig(t, cmrio.LegacyConfigPath(k), raw)
				if kind != "both" && kind != k {
					putCLIConfig(t, filepath.Join(cmrio.ConfigRoot(), name), raw)
				}
			}
			var out, stderr bytes.Buffer
			code := run([]string{"recommend", "--role", "developer", "--task-class", "code", "--candidates", candidates, "--json"}, &out, &stderr)
			if code != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
			want := ""
			// Catalog is loaded before policy.
			for _, k := range []string{"catalog", "policy"} {
				if kind == "both" || kind == k {
					name := "routing.toml"
					if k == "catalog" {
						name = "catalog.json"
					}
					want += fmt.Sprintf("cmr: reading legacy %s %s; move it to %s (cmr migrate)\n", k, cmrio.LegacyConfigPath(k), filepath.Join(cmrio.ConfigRoot(), name))
				}
			}
			if stderr.String() != want {
				t.Fatalf("got %q, want %q", stderr.String(), want)
			}
		})
	}
}

// Every policy layer is recorded in shadow metadata; inferred legacy shadow and
// explicit shadow preserve both child's byte streams and exit status exactly.
func TestSpawnPolicySourceAndExactOutput(t *testing.T) {
	for _, layer := range []string{"flag", "user", "system", "legacy", "default"} {
		t.Run(layer, func(t *testing.T) {
			root := cliEnvironment(t)
			candidates := candidateFile(t, root, false)
			policy := ""
			switch layer {
			case "flag":
				policy = filepath.Join(root, "explicit.toml")
			case "user":
				policy = filepath.Join(cmrio.ConfigRoot(), "routing.toml")
			case "system":
				policy = filepath.Join(cmrio.SystemConfigRoot, "routing.toml")
			case "legacy":
				policy = cmrio.LegacyConfigPath("policy")
			}
			if policy != "" {
				putCLIConfig(t, policy, []byte("mode = 'shadow'\n"))
			}
			binary := filepath.Join(root, "task-board")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'child stdout\\000end'\nprintf 'child stderr\\nend' >&2\nexit 17\n"), 0700); err != nil {
				t.Fatal(err)
			}
			var childOut, childErr bytes.Buffer
			child := exec.Command(binary, "spawn", "TASK", "--role=developer")
			child.Stdout, child.Stderr = &childOut, &childErr
			if err := child.Run(); err == nil {
				t.Fatal("fake child did not fail")
			}
			args := []string{"spawn", "--task-class", "code", "--candidates", candidates}
			if layer == "default" {
				args = append(args, "--mode", "shadow")
			}
			if layer == "flag" {
				args = append(args, "--policy", policy, "--mode", "shadow")
			}
			args = append(args, "--", "TASK", "--role=developer")
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 17 || !bytes.Equal(out.Bytes(), childOut.Bytes()) || !bytes.Equal(stderr.Bytes(), childErr.Bytes()) {
				t.Fatal(code, out.String(), stderr.String())
			}
			observations := readShadowObservations(t)
			if len(observations) != 1 || observations[0].PolicySource != layer || observations[0].TaskBoardExitCode != 17 {
				t.Fatal(observations)
			}
		})
	}
}

// Detached advice is silent and records the legacy source instead of emitting
// a deprecation notice or launching the child again.
func TestShadowFinishLegacySource(t *testing.T) {
	root := cliEnvironment(t)
	candidates := candidateFile(t, root, false)
	putCLIConfig(t, cmrio.LegacyConfigPath("policy"), []byte("mode = 'shadow'"))
	var out, stderr bytes.Buffer
	if code := run([]string{"shadow-finish", "--exit-status", "7", "--candidates", candidates, "--task-class", "code", "--", "TASK", "--role=developer"}, &out, &stderr); code != 0 || out.Len() != 0 || stderr.Len() != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	if got := readShadowObservations(t); len(got) != 1 || got[0].PolicySource != "legacy" || got[0].TaskBoardExitCode != 7 {
		t.Fatal(got)
	}
}

// Legacy and new logs merge chronologically, exact duplicates count once, and
// both roots contribute to totals and ordered fail-open details after filtering.
func TestShadowReportMergesLegacy(t *testing.T) {
	cliEnvironment(t)
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	older := newShadowObservation(recommendOptions{role: "developer", taskClass: "code", policySource: "legacy"}, spawnArguments{}, recommend.DefaultPolicy(), recommend.DecisionRecord{}, &Refusal{Code: "preflight_failed"}, 7, at)
	newer := older
	newer.Time = at.Add(time.Hour)
	newer.PolicySource = "user"
	newest := older
	newest.Time = at.Add(2 * time.Hour)
	newest.PolicySource = "system"
	for _, pair := range []struct {
		root string
		o    shadowObservation
	}{
		{cmrio.StateRoot(), newest}, {cmrio.StateRoot(), newer},
		{cmrio.LegacyStateRoot(), older}, {cmrio.LegacyStateRoot(), newer},
	} {
		if err := appendShadowObservation(pair.root, pair.o); err != nil {
			t.Fatal(err)
		}
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"shadow", "report", "--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var got shadowReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Totals.Observations != 3 || got.Totals.FailOpenLaunches != 3 || got.Totals.NonzeroTaskBoardExits != 3 || !reflect.DeepEqual(got.FailOpenLaunches, []shadowObservation{older, newer, newest}) || got.FailOpenByCode[0].Count != 3 {
		t.Fatal(got)
	}
	since := at.Add(time.Hour)
	filtered, err := readShadowReport(cmrio.StateRoot(), &since, cmrio.LegacyStateRoot())
	if err != nil || filtered.Totals.Observations != 2 {
		t.Fatal(filtered, err)
	}
}

// Migration JSON exposes copies, no-ops and typed conflicts without overwriting;
// dry-run never writes and invalid command flags refuse before doing any IO.
func TestMigrateCLI(t *testing.T) {
	cliEnvironment(t)
	old := cmrio.LegacyConfigPath("policy")
	putCLIConfig(t, old, []byte("fanout_k = 2"))
	var out, stderr bytes.Buffer
	if code := run([]string{"migrate", "--dry-run", "--json"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), `"status":"copy"`) {
		t.Fatal(code, out.String(), stderr.String())
	}
	if _, err := os.Stat(cmrio.ConfigRoot()); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote root")
	}
	for _, status := range []string{"copied", "unchanged"} {
		out.Reset()
		if code := run([]string{"migrate", "--json"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), `"status":"`+status+`"`) {
			t.Fatal(code, out.String(), stderr.String())
		}
	}
	putCLIConfig(t, old, []byte("fanout_k = 4"))
	out.Reset()
	if code := run([]string{"migrate", "--json"}, &out, &stderr); code != 2 || !strings.Contains(out.String(), `"code":"cmr_migrate_conflict"`) || !strings.Contains(out.String(), `"status":"conflict"`) {
		t.Fatal(code, out.String(), stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(cmrio.ConfigRoot(), "routing.toml"))
	if err != nil || string(got) != "fanout_k = 2" {
		t.Fatal("overwritten", string(got), err)
	}
	for _, args := range [][]string{{"config"}, {"config", "show", "extra", "--json"}, {"config", "show", "--unknown", "--json"}, {"migrate", "--unknown", "--json"}, {"migrate", "extra", "--json"}} {
		out.Reset()
		stderr.Reset()
		if code := run(args, &out, &stderr); code != 2 || !strings.Contains(out.String()+stderr.String(), "cmr_invalid_arguments") {
			t.Fatal(args, code, out.String(), stderr.String())
		}
	}
}

// Relative overlays remain relative to the working directory after moving a
// policy; missing cwd overlays refuse rather than using a policy-directory file.
func TestSharedPolicyRelativeOverlay(t *testing.T) {
	root := cliEnvironment(t)
	candidates := candidateFile(t, root, false)
	raw, err := os.ReadFile("../../pkg/recommend/testdata/fictional-overlay.json")
	if err != nil {
		t.Fatal(err)
	}
	putCLIConfig(t, filepath.Join(cmrio.ConfigRoot(), "routing.toml"), []byte("catalog_overlay = 'overlay.json'"))
	putCLIConfig(t, filepath.Join(cmrio.ConfigRoot(), "overlay.json"), raw)
	t.Chdir(root)
	args := []string{"recommend", "--role", "developer", "--task-class", "code", "--candidates", candidates, "--json"}
	var out, stderr bytes.Buffer
	if code := run(args, &out, &stderr); code != 2 || !strings.Contains(out.String(), "invalid_catalog_overlay") {
		t.Fatal(code, out.String(), stderr.String())
	}
	putCLIConfig(t, filepath.Join(root, "overlay.json"), raw)
	out.Reset()
	stderr.Reset()
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
}

// Decisions and evidence write only into the shared roots; malformed legacy
// usage state cannot contaminate a cold new cache.
func TestSharedStateAndDataWrites(t *testing.T) {
	root := cliEnvironment(t)
	candidates := candidateFile(t, root, false)
	putCLIConfig(t, filepath.Join(cmrio.LegacyStateRoot(), "usage"), []byte("blocked legacy usage"))
	var out, stderr bytes.Buffer
	if code := run([]string{"recommend", "--role", "developer", "--task-class", "code", "--candidates", candidates, "--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	paths, err := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	if _, err := os.Stat(filepath.Join(cmrio.LegacyStateRoot(), "decisions")); !os.IsNotExist(err) {
		t.Fatal("legacy decision write", err)
	}
	out.Reset()
	stderr.Reset()
	if code := run([]string{"evidence", "import", evidenceFixture(t, "roundtrip.input.json"), "--registry", evidenceFixture(t, "registry.json"), "--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cmrio.DataRoot(), "evidence", "current")); err != nil {
		t.Fatal("missing shared evidence", err)
	}
	if _, err := os.Stat(cmrio.LegacyDataRoot()); !os.IsNotExist(err) {
		t.Fatal("legacy data write", err)
	}
}
