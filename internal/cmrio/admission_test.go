package cmrio

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestPreflightRequiresExplicitCeilingEvidence(t *testing.T) {
	for _, ceiling := range []string{"absent", "null", "{}", `{"configured":null}`, `{"configured":"false"}`, `{"configured":0}`, `{"migration_required":false}`} {
		t.Run(ceiling, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("PATH", root)
			body := `{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"}`
			if ceiling != "absent" {
				body += `,"resolved_role_ceiling":` + ceiling
			}
			body += `}`
			if err := os.WriteFile(filepath.Join(root, "task-board"), []byte("#!/bin/sh\n/bin/cat <<'PAYLOAD'\n"+body+"\nPAYLOAD\n"), 0700); err != nil {
				t.Fatal(err)
			}
			catalog := recommend.Catalog{Rows: []recommend.CatalogRow{{Candidate: recommend.Candidate{Runtime: "codex", Model: "model", Effort: "high"}}}}
			candidates, _, err := Discover("", "developer", "codex", catalog)
			var refusal *recommend.Refusal
			if !errors.As(err, &refusal) || refusal.Code != "invalid_admission" || len(candidates) != 0 {
				t.Fatal("missing evidence widened admission", candidates, err)
			}
		})
	}
}

func TestPreflightAuthority(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)
	t.Setenv("HOME", root)
	catalog, err := recommend.LoadCatalog(recommend.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":true,"admitted_pairs":{"provider":"codex","models":[{"id":"gpt-6-astra","efforts":["medium","high"]}]}},"workload_class_recommendation":{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":[{"runtime":"codex","model":"gpt-6-astra","reasoning_effort":"medium"},{"runtime":"codex","model":"outside","reasoning_effort":"high"}]}}`
	fake := filepath.Join(root, "task-board")
	log := filepath.Join(root, "argv")
	t.Setenv("FAKE_LOG", log)
	write := func(body string) {
		t.Helper()
		script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$FAKE_LOG\"\n/bin/cat <<'PAYLOAD'\n" + body + "\nPAYLOAD\n"
		if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(body)
	candidates, source, err := Discover("", "developer", "", catalog)
	if err != nil || source != "spawn-preflight" || len(candidates) != 1 || candidates[0].Effort != "medium" {
		t.Fatal(candidates, source, err)
	}
	logBytes, _ := os.ReadFile(log)
	if !strings.Contains(string(logBytes), "--no-update-check\nq\nproject_config(view=spawn-preflight, role=developer)") {
		t.Fatalf("argv: %s", logBytes)
	}
	var wire map[string]any
	if err = json.Unmarshal([]byte(body), &wire); err != nil {
		t.Fatal(err)
	}
	wire["enabled"] = false
	b, _ := json.Marshal(wire)
	write(string(b))
	candidates, _, err = Discover("", "developer", "", catalog)
	if err != nil || len(candidates) != 0 {
		t.Fatal("disabled authority widened", candidates, err)
	}
	write("not JSON")
	if _, _, err = Discover("", "developer", "", catalog); err == nil {
		t.Fatal("bad preflight widened")
	}
	write(body)
	if _, _, err = Discover("", "bad)query", "", catalog); err == nil {
		t.Fatal("unsafe role")
	}
	// Explicit candidate files bypass even a malformed task-board response.
	file := filepath.Join(root, "candidates.json")
	os.WriteFile(file, []byte(`[]`), 0600)
	write("invalid")
	candidates, source, err = Discover(file, "developer", "", catalog)
	if err != nil || len(candidates) != 0 || source != "candidates-file" {
		t.Fatal(candidates, source, err)
	}
}
func TestPathCatalogFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)
	t.Setenv("HOME", root)
	// These executables are resolution evidence only; invoking them would fail.
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte("#!/bin/sh\nexit 88\n"), 0700); err != nil {
		t.Fatal(err)
	}
	catalog, err := recommend.LoadCatalog(recommend.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	candidates, source, err := Discover("", "developer", "", catalog)
	if err != nil || len(candidates) == 0 || source != "path-catalog" {
		t.Fatal(candidates, source, err)
	}
	for _, c := range candidates {
		if c.Runtime != "codex" {
			t.Fatal(c)
		}
	}
}
func TestIndeterminatePreflight(t *testing.T) {
	body := `{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":true,"admitted_pairs":{"provider":"codex","models":[]}},"workload_class_recommendation":{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":false},"unresolved_reason":"unreadable"}}`
	p, err := decodePreflight([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.candidates("codex", recommend.Catalog{}); err == nil {
		t.Fatal("indeterminate available set")
	}
}

func TestRoleOnlyPreflightWithCanonicalAdmission(t *testing.T) {
	body := `{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":true,"admitted_pairs":{"provider":"codex","models":[{"id":"gpt-6.1-sol","efforts":["high"]}]}},"workload_class_recommendation":{"configured":true,"class_resolved":false,"unresolved_reason":"workload_class_derivation_input_required"}}`
	p, err := decodePreflight([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := p.candidates("codex", recommend.Catalog{})
	if err != nil || len(candidates) != 1 || candidates[0].Model != "gpt-6.1-sol" {
		t.Fatal(candidates, err)
	}
}

func TestPreflightNoEffortAxis(t *testing.T) {
	body := `{"enabled":true,"role":"developer","providers":{"allowed":["agy"],"target":"agy"},"resolved_role_ceiling":{"configured":true,"admitted_pairs":{"provider":"agy","models":[{"id":"gemini-3.8-flash-high","efforts":[""]}]}},"workload_class_recommendation":{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":[{"runtime":"agy","model":"gemini-3.8-flash-high"}]}}`
	p, err := decodePreflight([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := p.candidates("agy", recommend.Catalog{})
	if err != nil || len(candidates) != 1 || candidates[0].Effort != "none" {
		t.Fatal(candidates, err)
	}
}

// An unconfigured role ceiling admits every model and effort of an allowed
// provider: the catalog rows of that runtime, and nothing of other runtimes.
func TestUnconfiguredCeilingAdmitsCatalogRowsOfAllowedRuntime(t *testing.T) {
	body := `{"enabled":true,"role":"developer","providers":{"allowed":["codex","claude"],"target":"codex"},"resolved_role_ceiling":{"configured":false}}`
	p, err := decodePreflight([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	cat := recommend.Catalog{Rows: []recommend.CatalogRow{
		{Candidate: recommend.Candidate{Runtime: "codex", Model: "gpt-6.1-sol", Effort: "high"}},
		{Candidate: recommend.Candidate{Runtime: "codex", Model: "gpt-6-astra", Effort: "medium"}},
		{Candidate: recommend.Candidate{Runtime: "claude", Model: "claude-sonnet-5-5", Effort: "high"}},
	}}
	got, err := p.candidates("codex", cat)
	if err != nil || len(got) != 2 || got[0].Runtime != "codex" || got[1].Runtime != "codex" {
		t.Fatal(got, err)
	}
	p.Providers.Allowed = []string{"claude"}
	if got, err = p.candidates("codex", cat); err != nil || len(got) != 0 {
		t.Fatal("a provider outside the allow-set must admit nothing", got, err)
	}
}
