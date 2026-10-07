package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/routing"
)

func cliFixture(t *testing.T) (string, string, string) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/headroom/01_expiring_first/input.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := routing.LoadBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := routing.Route(b)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	decision := filepath.Join(dir, "decision.json")
	snapshot := filepath.Join(dir, "snapshot.json")
	for p, v := range map[string]any{input: b, decision: r.Decision, snapshot: b.Snapshot} {
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(p, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return input, decision, snapshot
}
func TestRoutingCommands(t *testing.T) {
	input, decision, snapshot := cliFixture(t)
	tests := []struct {
		name     string
		args     []string
		contains string
	}{
		{"route", []string{"route", "--input", input}, "effective=b"},
		{"route_json", []string{"route", "--json", "--input", input}, `"outcome":"selected"`},
		{"explain", []string{"explain", "--decision", decision, "--input", input}, "freshness=fresh"},
		{"explain_json", []string{"explain", "--decision", decision, "--input", input, "--json"}, `"slack_bp":7000`},
		{"replay", []string{"replay", "--decision", decision, "--input", input}, "equal=true"},
		{"replay_json", []string{"replay", "--json", "--decision", decision, "--input", input}, `"equal":true`},
		{"headroom", []string{"headroom", "explain", "--snapshot", snapshot, "--role", "reviewer"}, "class=subscription"},
		{"headroom_json", []string{"headroom", "explain", "--snapshot", snapshot, "--json"}, `"band":7`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			if code := run(tc.args, &out, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), tc.contains) {
				t.Fatalf("code=%d out=%s err=%s", code, &out, &stderr)
			}
			if hasJSON(tc.args) && !json.Valid(out.Bytes()) {
				t.Fatal("invalid JSON")
			}
		})
	}
	for _, tc := range tests {
		t.Run(tc.name+"_write_failure", func(t *testing.T) {
			var out failFirstWriter
			var stderr bytes.Buffer
			if code := run(tc.args, &out, &stderr); code != 2 {
				t.Fatalf("code=%d", code)
			}
			if hasJSON(tc.args) {
				if !strings.Contains(out.String(), "cmr_output_failed") || stderr.Len() != 0 {
					t.Fatalf("out=%s err=%s", out.String(), stderr.String())
				}
			} else if !strings.Contains(stderr.String(), "cmr_output_failed") {
				t.Fatal(stderr.String())
			}
		})
	}
}
func TestRoutingCLIRefusals(t *testing.T) {
	input, decision, snapshot := cliFixture(t)
	dir := t.TempDir()
	malformed := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(malformed, []byte(`{"schema_version":"v1","extra":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args []string
		code string
	}{
		{[]string{"route"}, "cmr_invalid_arguments"},
		{[]string{"route", "--input="}, "cmr_invalid_arguments"},
		{[]string{"route", "--wat", input}, "cmr_invalid_arguments"},
		{[]string{"route", "--input", input, "--input", input}, "cmr_invalid_arguments"},
		{[]string{"route", "--input", filepath.Join(dir, "missing")}, "cmr_input_failed"},
		{[]string{"route", "--input", malformed}, "contract_unknown_field"},
		{[]string{"explain", "--input", input}, "cmr_invalid_arguments"},
		{[]string{"replay", "--decision", decision}, "cmr_invalid_arguments"},
		{[]string{"headroom"}, "cmr_invalid_arguments"},
		{[]string{"headroom", "bad"}, "cmr_invalid_arguments"},
		{[]string{"headroom", "explain", "--snapshot", snapshot, "--policy", malformed}, "contract_unknown_field"},
	}
	for _, tc := range tests {
		for _, asJSON := range []bool{false, true} {
			args := append([]string{}, tc.args...)
			if asJSON {
				args = append(args, "--json")
			}
			var out, stderr bytes.Buffer
			code := run(args, &out, &stderr)
			if code != 2 {
				t.Fatalf("%v code=%d", args, code)
			}
			if asJSON {
				if stderr.Len() != 0 || !json.Valid(out.Bytes()) || !strings.Contains(out.String(), tc.code) {
					t.Fatalf("%v out=%s err=%s", args, &out, &stderr)
				}
			} else if out.Len() != 0 || !strings.Contains(stderr.String(), tc.code) {
				t.Fatalf("%v out=%s err=%s", args, &out, &stderr)
			}
		}
	}
}
func TestCLIReplayMismatchAndOff(t *testing.T) {
	input, decision, snapshot := cliFixture(t)
	raw, err := os.ReadFile(decision)
	if err != nil {
		t.Fatal(err)
	}
	d, err := routing.LoadDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	d.SelectedCandidate.Model = "changed"
	raw, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(decision, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := run([]string{"replay", "--decision", decision, "--input", input, "--json"}, &out, &stderr)
	if code != 1 || stderr.Len() != 0 || !strings.Contains(out.String(), "$.selected_candidate.model") {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &stderr)
	}
	out.Reset()
	stderr.Reset()
	code = run([]string{"explain", "--decision", decision, "--input", input, "--json"}, &out, &stderr)
	if code != 2 || !strings.Contains(out.String(), "contract_decision_id_mismatch") {
		t.Fatalf("code=%d out=%s", code, &out)
	}
	raw, err = os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	b, err := routing.LoadBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	b.Policy.Mode = routing.ModeOff
	b.Snapshot.ConfigOrder = []string{"b", "a"}
	b.Snapshot.AsOf = 0 // Off preserves the baseline before snapshot validation.
	raw, err = json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	code = run([]string{"route", "--input", input, "--json"}, &out, &stderr)
	if code != 0 || strings.Contains(out.String(), `"decision"`) || !strings.Contains(out.String(), `"id":"b"`) {
		t.Fatalf("off code=%d out=%s", code, &out)
	}
	p := filepath.Join(t.TempDir(), "policy.json")
	if err = os.WriteFile(p, []byte(`{"schema_version":"v1","mode":"recommend","headroom":{"enabled":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code = run([]string{"headroom", "explain", "--snapshot", snapshot, "--policy", p, "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("policy code=%d out=%s err=%s", code, &out, &stderr)
	}
}
