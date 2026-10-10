package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpawnFromCanonicalPreflight(t *testing.T) {
	root := cliEnvironment(t)
	log := filepath.Join(root, "argv")
	t.Setenv("FAKE_LOG", log)
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$FAKE_LOG"
if [ "$1" = '--no-update-check' ]; then
/bin/cat <<'PAYLOAD'
{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":true,"contract_version":"spawn-policy-v2","model_criterion":"less_or_equal","admitted_pairs":{"provider":"codex","models":[{"id":"gpt-6.1-sol","efforts":["high"]}]}},"workload_class_recommendation":{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":[{"runtime":"codex","model":"gpt-6.1-sol","reasoning_effort":"high"}]}}
PAYLOAD
else
/bin/sleep 1
fi
`
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"spawn", "--task-class", "code.implement", "--", "TASK", "--role", "developer", "--background", "--task-path", "implement it"}, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"project_config(view=spawn-preflight, role=developer)", "spawn\nTASK\n--role\ndeveloper\n--background\n--task-path\nimplement it\n--agent\ncodex\n--model\ngpt-6.1-sol\n--reasoning-effort\nhigh\n--selection-rationale\ncmr:sha256:"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in %s", want, b)
		}
	}
}

func TestShadowPreflightIgnoresCallerAgent(t *testing.T) {
	root := cliEnvironment(t)
	log := fakeTaskBoard(t, root)
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$FAKE_LOG"
if [ "$1" = '--no-update-check' ]; then
/bin/cat <<'PAYLOAD'
{"enabled":true,"role":"reviewer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":true,"contract_version":"spawn-policy-v2","model_criterion":"equal","admitted_pairs":{"provider":"codex","models":[{"id":"gpt-6.1-sol","efforts":["high"]}]}},"workload_class_recommendation":{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":[{"runtime":"codex","model":"gpt-6.1-sol","reasoning_effort":"high"}]}}
PAYLOAD
else
/bin/sleep 1
fi
`
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"spawn", "--mode", "shadow", "--task-class", "review.code", "--", "TASK", "--role=reviewer", "--agent=claude", "--model=caller-model", "--reasoning-effort=max", "--background", "--board-dir=board with spaces"}, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--board-dir=board with spaces\nq\nproject_config(view=spawn-preflight, role=reviewer)\n", "spawn\nTASK\n--role=reviewer\n--agent=claude\n--model=caller-model\n--reasoning-effort=max\n--background\n--board-dir=board with spaces\n"} {
		if !strings.Contains(string(raw), want) {
			t.Fatal("missing unconstrained preflight or original launch", want, string(raw))
		}
	}
	o := readShadowObservations(t)[0]
	if o.Role != "reviewer" || o.CMRPick != (shadowPick{Runtime: "codex", Model: "gpt-6.1-sol", Effort: "high"}) || o.Agreement != "different_runtime" || o.CMRError != nil {
		t.Fatalf("%+v", o)
	}
}
