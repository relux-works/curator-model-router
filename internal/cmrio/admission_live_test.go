package cmrio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func fakePreflight(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("PATH", root)
	t.Setenv("PREFLIGHT_ROOT", root)
	if err := os.WriteFile(filepath.Join(root, "task-board"), []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPreflightTimeoutAndFailureCodes(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{"exec /bin/sleep 5\n", "preflight_timeout"},
		{"printf 'private provider output' >&2\nexit 7\n", "preflight_failed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			fakePreflight(t, tc.body)
			start := time.Now()
			got, _, err := DiscoverWithOptions(context.Background(), 100*time.Millisecond, "", "developer", "", recommend.Catalog{})
			var r *recommend.Refusal
			if !errors.As(err, &r) || r.Code != tc.code || len(got) != 0 || strings.Contains(r.Message, "private") {
				t.Fatal(got, err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("preflight exceeded its bound")
			}
		})
	}
}

func TestPreflightReusesCompleteRoleResponse(t *testing.T) {
	root := fakePreflight(t, `printf 'call\n' >> "$PREFLIGHT_ROOT/calls"
printf '%s\n' '{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":false}}'
`)
	cat := recommend.Catalog{Rows: []recommend.CatalogRow{{Candidate: recommend.Candidate{Runtime: "codex", Model: "fake", Effort: "high"}}}}
	got, _, err := Discover("", "developer", "", cat)
	calls, _ := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil || len(got) != 1 || string(calls) != "call\n" {
		t.Fatal(got, err, string(calls))
	}
}

// A filesystem barrier proves overlap and caps concurrency without depending on
// how fast the host is: the first four providers must all start before any exit.
func TestPreflightFallbackConcurrentCappedAndDeterministic(t *testing.T) {
	agents := []string{"a", "b", "c", "d", "e", "f", "g"}
	root := fakePreflight(t, `case "$3" in
 *agent=*)
  agent=${3##*agent=}; agent=${agent%)}
  : > "$PREFLIGHT_ROOT/$agent.started"
  case "$agent" in
   a|b|c|d)
    i=0
    while [ ! -e "$PREFLIGHT_ROOT/a.started" ] || [ ! -e "$PREFLIGHT_ROOT/b.started" ] || [ ! -e "$PREFLIGHT_ROOT/c.started" ] || [ ! -e "$PREFLIGHT_ROOT/d.started" ]; do
     i=$((i+1)); [ "$i" -lt 200 ] || exit 90
     /bin/sleep 0.01
    done
    [ ! -e "$PREFLIGHT_ROOT/e.started" ] && [ ! -e "$PREFLIGHT_ROOT/f.started" ] && [ ! -e "$PREFLIGHT_ROOT/g.started" ] || exit 91
    : > "$PREFLIGHT_ROOT/$agent.checked"
    i=0
    while [ ! -e "$PREFLIGHT_ROOT/a.checked" ] || [ ! -e "$PREFLIGHT_ROOT/b.checked" ] || [ ! -e "$PREFLIGHT_ROOT/c.checked" ] || [ ! -e "$PREFLIGHT_ROOT/d.checked" ]; do
     i=$((i+1)); [ "$i" -lt 200 ] || exit 92
     /bin/sleep 0.01
    done
   ;;
  esac
  printf '{"enabled":true,"role":"developer","providers":{"allowed":["%s"],"target":"%s"},"resolved_role_ceiling":{"configured":false}}\n' "$agent" "$agent"
 ;;
 *) printf '%s\n' '{"enabled":true,"role":"developer","providers":{"allowed":["a","b","c","d","e","f","g"]}}' ;;
esac
`)
	cat := recommend.Catalog{}
	for _, a := range agents {
		cat.Rows = append(cat.Rows, recommend.CatalogRow{Candidate: recommend.Candidate{Runtime: a, Model: "fake", Effort: "high"}})
	}
	admission := &recommend.AdmissionContext{}
	got, source, err := DiscoverWithOptions(context.Background(), 5*time.Second, "", "developer", "", cat, admission)
	if err != nil || source != "spawn-preflight" || len(got) != 7 {
		t.Fatal(got, source, err)
	}
	for i, a := range agents {
		if got[i].Runtime != a {
			t.Fatal("nondeterministic output", got)
		}
		if _, err := os.Stat(filepath.Join(root, a+".started")); err != nil {
			t.Fatal(err)
		}
	}
	if len(admission.RationaleRequired) != 7 {
		t.Fatal(fmt.Sprint(admission))
	}
}
