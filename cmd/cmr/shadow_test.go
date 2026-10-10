package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func shadowString(s string) *string { return &s }
func completeShadowPair(runtime, model, effort string) shadowPair {
	return shadowPair{shadowString(runtime), shadowString(model), shadowString(effort)}
}

func TestShadowAgreement(t *testing.T) {
	pick := shadowPick{Runtime: "codex", Model: "gpt-6.1-sol", Effort: "high"}
	for _, tc := range []struct {
		name   string
		caller shadowPair
		pick   shadowPick
		want   string
	}{
		{"exact", completeShadowPair("codex", "gpt-6.1-sol", "high"), pick, "exact"},
		{"alias", completeShadowPair("codex", "sol", "high"), pick, "exact"},
		{"effort", completeShadowPair("codex", "gpt-6.1-sol", "medium"), pick, "same_model_other_effort"},
		{"model", completeShadowPair("codex", "other", "high"), pick, "same_runtime_other_model"},
		{"runtime", completeShadowPair("claude", "other", "max"), pick, "different_runtime"},
		{"all_defaults", shadowPair{}, pick, "unknown_caller_default"},
		{"runtime_default", shadowPair{Model: shadowString("gpt-6.1-sol"), Effort: shadowString("high")}, pick, "unknown_caller_default"},
		{"model_default", shadowPair{Runtime: shadowString("codex"), Effort: shadowString("high")}, pick, "unknown_caller_default"},
		{"effort_default", shadowPair{Runtime: shadowString("codex"), Model: shadowString("gpt-6.1-sol")}, pick, "unknown_caller_default"},
		{"refusal_before_default", shadowPair{}, shadowPick{Code: "no_qualified_candidate"}, "no_cmr_pick"},
		{"missing_pick", completeShadowPair("codex", "gpt-6.1-sol", "high"), shadowPick{}, "no_cmr_pick"},
		{"error", completeShadowPair("codex", "gpt-6.1-sol", "high"), shadowPick{Code: "invalid_catalog"}, "no_cmr_pick"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shadowAgreement(tc.caller, tc.pick); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func readShadowObservations(t *testing.T) []shadowObservation {
	t.Helper()
	data, err := os.ReadFile(shadowObservationPath(cmrio.StateRoot()))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatal("missing JSONL terminator")
	}
	observations := []shadowObservation{}
	for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
		var observation shadowObservation
		if err := json.Unmarshal(line, &observation); err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observation)
	}
	return observations
}

func TestShadowUnconstrainedPickAndOriginalBytes(t *testing.T) {
	for _, cmrLocks := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconstrained", true: "cmr_flags_apply"}[cmrLocks], func(t *testing.T) {
			root := cliEnvironment(t)
			candidates := candidateFile(t, root, false)
			log := filepath.Join(root, "argv-bytes")
			t.Setenv("FAKE_LOG", log)
			t.Setenv("OBSERVATIONS", shadowObservationPath(cmrio.StateRoot()))
			script := "#!/bin/sh\n[ ! -e \"$OBSERVATIONS\" ] || exit 99\nprintf '%s\\0' \"$@\" > \"$FAKE_LOG\"\n/bin/sleep 1\nexit 17\n"
			if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			options := []string{"--task-class", "code.implement", "--candidates", candidates}
			if cmrLocks {
				options = append(options, "--agent", "codex", "--model", "sol", "--reasoning-effort", "high")
			}
			baselineOptions, err := recommendFlags("recommend", append(append([]string{}, options...), "--role", "reviewer"), false)
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := recommendation(baselineOptions)
			if err != nil || baseline.Recommendation.Selected == nil {
				t.Fatal(baseline, err)
			}
			// A caller pair absent from the catalog must not constrain the advisory pick.
			forwarded := []string{"TASK\nwith spaces", "--role=reviewer", "--agent", "claude", "--model=caller-model", "--reasoning-effort", "max", "--task-path", "literal\n--model=hidden", "--background", "--", "--agent=literal", ""}
			args := append([]string{"spawn", "--mode", "shadow"}, options...)
			args = append(args, "--")
			args = append(args, forwarded...)
			var out, stderr bytes.Buffer
			before := time.Now().UTC()
			if status := run(args, &out, &stderr); status != 17 {
				t.Fatal(status, out.String(), stderr.String())
			}
			actual, err := os.ReadFile(log)
			expected := []byte(strings.Join(append([]string{"spawn"}, forwarded...), "\x00") + "\x00")
			if err != nil || !bytes.Equal(actual, expected) {
				t.Fatalf("launch bytes changed: %q != %q (%v)", actual, expected, err)
			}
			observations := readShadowObservations(t)
			if len(observations) != 1 {
				t.Fatal(observations)
			}
			o := observations[0]
			c := baseline.Recommendation.Selected
			expectedPick := shadowPick{Runtime: c.Runtime, Model: c.Model, Effort: c.Effort}
			if o.SchemaVersion != "shadow-observation-v1" || o.DecisionID == nil || o.CMRPick != expectedPick || o.Agreement == "exact" || o.Agreement == "no_cmr_pick" || o.CMRError != nil || o.TaskBoardExitCode != 17 || o.Time.Before(before) || o.Time.After(time.Now().UTC()) || o.Time.Location() != time.UTC {
				t.Fatalf("observation: %+v", o)
			}
			if o.Role != "reviewer" || o.TaskClass != "code.implement" || o.Difficulty != "standard" || o.Sensitivity != "normal" || o.Budget != "balanced" || !reflect.DeepEqual(o.CallerPair, completeShadowPair("claude", "caller-model", "max")) {
				t.Fatalf("profile/caller: %+v", o)
			}
			raw, err := os.ReadFile(filepath.Join(cmrio.StateRoot(), "decisions", strings.TrimPrefix(*o.DecisionID, "sha256:")+".json"))
			var record recommend.DecisionRecord
			if err != nil || json.Unmarshal(raw, &record) != nil {
				t.Fatal(err, string(raw))
			}
			wantLocks := recommend.Locks{}
			if cmrLocks {
				wantLocks = recommend.Locks{Agent: "codex", Model: "gpt-6.1-sol", Effort: "high"}
			}
			if record.Inputs.Locks != wantLocks {
				t.Fatal("caller flags leaked into locks", record.Inputs.Locks)
			}
		})
	}
}

func TestShadowObservationDefaultsErrorsAndRefusals(t *testing.T) {
	for _, scenario := range []string{"defaults", "policy_error", "refusal", "decision_write_error"} {
		t.Run(scenario, func(t *testing.T) {
			root := cliEnvironment(t)
			file := candidateFile(t, root, scenario == "refusal")
			log := fakeTaskBoard(t, root)
			t.Setenv("FAKE_EXIT", "23")
			args := []string{"spawn", "--mode", "shadow", "--task-class", "code.implement", "--candidates", file}
			if scenario == "policy_error" {
				args = append(args, "--policy", filepath.Join(root, "private-marker-secret.toml"))
			}
			if scenario == "decision_write_error" {
				if err := os.MkdirAll(cmrio.StateRoot(), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cmrio.StateRoot(), "decisions"), []byte("blocked"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			forwarded := []string{"TASK", "--role", "developer", "--background"}
			if scenario != "defaults" {
				forwarded = append(forwarded, "--agent=claude", "--model=caller-model", "--reasoning-effort=max")
			}
			args = append(args, "--")
			args = append(args, forwarded...)
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 23 {
				t.Fatal(code, out.String(), stderr.String())
			}
			raw, err := os.ReadFile(log)
			if err != nil || string(raw) != strings.Join(append([]string{"spawn"}, forwarded...), "\n")+"\n" {
				t.Fatal("launch changed", string(raw), err)
			}
			observations := readShadowObservations(t)
			if len(observations) != 1 {
				t.Fatal(observations)
			}
			o := observations[0]
			switch scenario {
			case "defaults":
				if o.CallerPair != (shadowPair{}) || o.Agreement != "unknown_caller_default" || o.CMRError != nil {
					t.Fatalf("%+v", o)
				}
			case "refusal":
				if o.CMRPick.Code != "no_qualified_candidate" || o.CMRError != nil || o.DecisionID == nil || o.Agreement != "no_cmr_pick" {
					t.Fatalf("%+v", o)
				}
			default:
				expectedCode := "invalid_policy"
				if scenario == "decision_write_error" {
					expectedCode = "cmr_decision_write_failed"
				}
				if o.CMRError == nil || o.CMRError.Code != expectedCode || o.CMRPick.Code != expectedCode || o.Agreement != "no_cmr_pick" {
					t.Fatalf("%+v", o)
				}
				encoded, _ := json.Marshal(o.CMRError)
				if bytes.Contains(encoded, []byte(root)) || bytes.Contains(encoded, []byte("private-marker-secret")) {
					t.Fatal("diagnostics leaked", string(encoded))
				}
			}
		})
	}
}

func TestShadowObservationWriteFailure(t *testing.T) {
	for _, status := range []string{"0", "23"} {
		t.Run(status, func(t *testing.T) {
			root := cliEnvironment(t)
			file := candidateFile(t, root, false)
			log := fakeTaskBoard(t, root)
			t.Setenv("FAKE_EXIT", status)
			if err := os.MkdirAll(cmrio.StateRoot(), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cmrio.StateRoot(), "shadow"), []byte("blocked"), 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			code := run([]string{"spawn", "--mode", "shadow", "--task-class", "code.implement", "--candidates", file, "--", "TASK", "--role=developer", "--background"}, &out, &stderr)
			if (status == "0" && code != 0) || (status == "23" && code != 23) {
				t.Fatal("observation failure changed status", code)
			}
			raw, err := os.ReadFile(log)
			if err != nil || string(raw) != "spawn\nTASK\n--role=developer\n--background\n" {
				t.Fatal("launch changed", string(raw), err)
			}
			if strings.Count(stderr.String(), "cmr:shadow warning: could not append observation\n") != 1 {
				t.Fatal("missing one-line warning", stderr.String())
			}
		})
	}
}

func TestShadowReportAggregation(t *testing.T) {
	cliEnvironment(t)
	at := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	pick := shadowPick{Runtime: "codex", Model: "gpt-6.1-sol", Effort: "high"}
	base := shadowObservation{SchemaVersion: "shadow-observation-v1", Time: at, Role: "developer", TaskClass: "code.implement", Difficulty: "standard", Sensitivity: "normal", Budget: "balanced", CMRPick: pick, CallerPair: completeShadowPair("codex", "gpt-6.1-sol", "high"), Agreement: "exact"}
	items := []shadowObservation{base}
	effort := base
	effort.CallerPair = completeShadowPair("codex", "gpt-6.1-sol", "medium")
	items = append(items, effort, effort)
	model := base
	model.CallerPair = completeShadowPair("codex", "other", "high")
	items = append(items, model)
	runtime := base
	runtime.CallerPair = completeShadowPair("claude", "other", "max")
	runtime.Role = "reviewer"
	items = append(items, runtime)
	unknown := base
	unknown.CallerPair = shadowPair{}
	items = append(items, unknown)
	refusal := base
	refusal.CMRPick = shadowPick{Code: "no_qualified_candidate"}
	items = append(items, refusal)
	failed := base
	failed.CMRError = &Refusal{Code: "invalid_policy", Message: "advisory recommendation failed"}
	failed.TaskBoardExitCode = 23
	// Deliberately leave an exact stored label and pick on this failed entry.
	items = append(items, failed)
	old := base
	old.Time = at.Add(-time.Second)
	items = append(items, old)
	for _, o := range items {
		if err := appendShadowObservation(cmrio.StateRoot(), o); err != nil {
			t.Fatal(err)
		}
	}
	var out, stderr bytes.Buffer
	args := []string{"shadow", "report", "--since", "2026-10-07T03:00:00+03:00", "--json"}
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	var report shadowReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	wantTotals := shadowTotals{Observations: 8, Comparisons: 5, Agreements: 1, Divergences: 4, UnknownCallerDefaults: 1, Refusals: 1, FailOpenLaunches: 1, NonzeroTaskBoardExits: 1}
	if report.SchemaVersion != "shadow-report-v1" || report.Since == nil || !report.Since.Equal(at) || report.Totals != wantTotals {
		t.Fatalf("%+v", report)
	}
	wantDistribution := map[string]int{"exact": 1, "same_model_other_effort": 2, "same_runtime_other_model": 1, "different_runtime": 1, "unknown_caller_default": 1, "no_cmr_pick": 2}
	if !reflect.DeepEqual(report.AgreementDistribution, wantDistribution) || len(report.TopDivergences) != 3 || report.TopDivergences[0].Count != 2 || len(report.Refusals) != 1 || report.Refusals[0].Code != "no_qualified_candidate" || len(report.FailOpenLaunches) != 1 || report.FailOpenLaunches[0].CMRError.Code != "invalid_policy" {
		t.Fatalf("%+v", report)
	}
	first := out.String()
	out.Reset()
	if code := run(args, &out, &stderr); code != 0 || out.String() != first {
		t.Fatal("JSON report unstable", code, out.String())
	}
	out.Reset()
	if code := run(args[:len(args)-1], &out, &stderr); code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"Shadow: 8 observations; 5 comparisons; 1 exact; 4 divergences", "FAIL-OPEN launches: 1", "Top divergences (by count):", "Refusals:\n  no_qualified_candidate: 1", "FAIL-OPEN launches (advisory errors; excluded from comparisons):", `error="invalid_policy" task_board_exit_code=23`} {
		if !strings.Contains(out.String(), want) {
			t.Fatal("missing human output", want, out.String())
		}
	}
	report, err := readShadowReport(cmrio.StateRoot(), nil)
	if err != nil || report.Totals.Observations != 9 {
		t.Fatal(report, err)
	}
}

func TestShadowReportEmptyInvalidAndOutputFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		setup    string
		want     int
		contains string
	}{
		{"empty", []string{"shadow", "report", "--json"}, "", 0, `"top_divergences":[],"refusals":[],"fail_open_by_code":[],"fail_open_launches":[]`},
		{"empty_since", []string{"shadow", "report", "--since=", "--json"}, "", 2, "cmr_invalid_arguments"},
		{"bad_since", []string{"shadow", "report", "--since", "yesterday", "--json"}, "", 2, "cmr_invalid_arguments"},
		{"missing_since_value", []string{"shadow", "report", "--since", "--json"}, "", 2, "cmr_invalid_arguments"},
		{"unknown_flag", []string{"shadow", "report", "--unknown", "--json"}, "", 2, "cmr_invalid_arguments"},
		{"no_subcommand", []string{"shadow", "--json"}, "", 2, "cmr_invalid_arguments"},
		{"positional", []string{"shadow", "report", "extra", "--json"}, "", 2, "cmr_invalid_arguments"},
		{"invalid_json", []string{"shadow", "report", "--json"}, "broken", 2, "cmr_shadow_invalid_observation"},
		{"missing_fields", []string{"shadow", "report", "--json"}, `{"schema_version":"shadow-observation-v1","time":"2026-10-07T00:00:00Z"}`, 2, "cmr_shadow_invalid_observation"},
		{"read_failure", []string{"shadow", "report", "--json"}, "directory", 2, "cmr_shadow_read_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cliEnvironment(t)
			path := shadowObservationPath(cmrio.StateRoot())
			if tc.setup != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if tc.setup == "directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte(tc.setup+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out, stderr bytes.Buffer
			code := run(tc.args, &out, &stderr)
			if code != tc.want || !strings.Contains(out.String()+stderr.String(), tc.contains) {
				t.Fatal(code, out.String(), stderr.String())
			}
		})
	}
	cliEnvironment(t)
	var out failFirstWriter
	var stderr bytes.Buffer
	if code := run([]string{"shadow", "report", "--json"}, &out, &stderr); code != 2 || !strings.Contains(out.String(), "cmr_output_failed") {
		t.Fatal(code, out.String())
	}
}

func TestShadowErrorSanitized(t *testing.T) {
	o := newShadowObservation(recommendOptions{role: "developer", taskClass: "code.fix"}, spawnArguments{}, recommend.DefaultPolicy(), recommend.DecisionRecord{}, errors.New("sensitive-provider-output\nprivate-path"), 0, time.Now())
	if o.CMRError == nil || o.CMRError.Code != "cmr_usage_failed" || strings.Contains(o.CMRError.Message, "sensitive") {
		t.Fatalf("%+v", o)
	}
}

func TestShadowConcurrentAppend(t *testing.T) {
	cliEnvironment(t)
	root := cmrio.StateRoot()
	base := shadowObservation{SchemaVersion: "shadow-observation-v1", Time: time.Now().UTC(), CMRPick: shadowPick{Code: "no_qualified_candidate"}}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- appendShadowObservation(root, base) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if observations := readShadowObservations(t); len(observations) != 32 {
		t.Fatal(len(observations))
	}
	info, err := os.Stat(shadowObservationPath(root))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
}

func TestShadowReportSkipsOnlyAnIncompleteFinalLine(t *testing.T) {
	cliEnvironment(t)
	root := cmrio.StateRoot()
	path := shadowObservationPath(root)
	o := shadowObservation{SchemaVersion: "shadow-observation-v1", Time: time.Now().UTC(), CMRPick: shadowPick{Code: "no_qualified_candidate"}}
	for i := 0; i < 2; i++ {
		if err := appendShadowObservation(root, o); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A complete record that lost only its newline still counts.
	if err := os.Truncate(path, info.Size()-1); err != nil {
		t.Fatal(err)
	}
	report, err := readShadowReport(root, nil)
	if err != nil || report.Totals.Observations != 2 || report.Totals.TruncatedTail {
		t.Fatal("newline-only truncation", report.Totals, err)
	}
	// An interrupted append leaves a partial record: it is excluded and flagged,
	// and every preceding complete observation is still reported.
	if err := os.Truncate(path, info.Size()-12); err != nil {
		t.Fatal(err)
	}
	report, err = readShadowReport(root, nil)
	if err != nil || report.Totals.Observations != 1 || !report.Totals.TruncatedTail {
		t.Fatal("partial final record", report.Totals, err)
	}
	var out bytes.Buffer
	if err := renderShadowReport(&out, report); err != nil || !strings.Contains(out.String(), "incomplete observation") {
		t.Fatal(out.String(), err)
	}
	// An oversized partial tail (beyond any line buffer) is still only a tail.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := appendShadowObservation(root, o); err != nil {
			t.Fatal(err)
		}
	}
	f1, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f1.WriteString(`{"role":"` + strings.Repeat("x", 5<<20)); err != nil {
		t.Fatal(err)
	}
	f1.Close()
	report, err = readShadowReport(root, nil)
	if err != nil || report.Totals.Observations != 2 || !report.Totals.TruncatedTail {
		t.Fatal("oversized partial tail", report.Totals, err)
	}
	// A malformed COMPLETE line is still an error, never silently skipped.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n{\"schema_version\":\"wrong\"}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := readShadowReport(root, nil); err == nil {
		t.Fatal("malformed complete line accepted")
	}
}
