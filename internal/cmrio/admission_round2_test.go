package cmrio

import (
	"errors"
	"fmt"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestWorkloadAuthorityNeverWidens(t *testing.T) {
	cat := recommend.Catalog{Rows: []recommend.CatalogRow{{Candidate: recommend.Candidate{Runtime: "codex", Model: "gpt-6.1-sol", Effort: "high"}}}}
	for _, configured := range []bool{false, true} {
		for _, tc := range []struct {
			name, workload string
			invalid        bool
			count          int
		}{
			{"missing", "", configured, 1},
			{"empty", `{}`, true, 0},
			{"unconfigured-null-availability", `{"configured":false,"available_pairs":null}`, true, 0},
			{"unconfigured-null-class", `{"configured":false,"class_resolved":null}`, true, 0},
			{"role-only-null-availability", `{"configured":true,"class_resolved":false,"unresolved_reason":"workload_class_derivation_input_required","available_pairs":null}`, true, 0},
			{"role-only-null-integrity", `{"configured":true,"class_resolved":false,"unresolved_reason":"workload_class_derivation_input_required","limit_read_integrity":null}`, true, 0},
			{"unconfigured", `{"configured":false}`, false, 1},
			{"role-only", `{"configured":true,"class_resolved":false,"unresolved_reason":"workload_class_derivation_input_required"}`, false, 1},
			{"reason-without-authority", `{"unresolved_reason":"workload_class_derivation_input_required"}`, true, 0},
			{"resolved-missing", `{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true}}`, true, 0},
			{"resolved-null", `{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":null}`, true, 0},
			{"resolved-empty", `{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":[]}`, false, 0},
			{"resolved-pair", `{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":[{"runtime":"codex","model":"gpt-6.1-sol","reasoning_effort":"high"}]}`, false, 1},
			{"missing-class", `{"configured":true,"limit_read_integrity":{"determinate":true},"available_pairs":[]}`, true, 0},
			{"missing-configured", `{"class_resolved":true,"limit_read_integrity":{"determinate":true},"available_pairs":[]}`, true, 0},
			{"missing-integrity", `{"configured":true,"class_resolved":true,"available_pairs":[]}`, true, 0},
			{"missing-determinate", `{"configured":true,"class_resolved":true,"limit_read_integrity":{},"available_pairs":[]}`, true, 0},
			{"indeterminate", `{"configured":true,"class_resolved":true,"limit_read_integrity":{"determinate":false},"available_pairs":[]}`, true, 0},
			{"contradictory-role-only", `{"configured":true,"class_resolved":true,"unresolved_reason":"workload_class_derivation_input_required"}`, true, 0},
			{"contradictory-unconfigured", `{"configured":false,"class_resolved":true,"available_pairs":[]}`, true, 0},
			{"unknown-unresolved", `{"configured":true,"class_resolved":false,"unresolved_reason":"unknown"}`, true, 0},
		} {
			t.Run(fmt.Sprintf("ceiling=%t/%s", configured, tc.name), func(t *testing.T) {
				body := fmt.Sprintf(`{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":%t,"admitted_pairs":{"provider":"codex","models":[{"id":"gpt-6.1-sol","efforts":["high"]}]}}`, configured)
				if tc.workload != "" {
					body += `,"workload_class_recommendation":` + tc.workload
				}
				body += `}`
				p, err := decodePreflight([]byte(body))
				if err != nil {
					t.Fatal(err)
				}
				got, err := p.candidates("codex", cat)
				if tc.invalid {
					var refusal *recommend.Refusal
					if !errors.As(err, &refusal) || refusal.Code != "invalid_admission" || len(got) != 0 {
						t.Fatal("missing authority widened admission", got, err)
					}
				} else if err != nil || len(got) != tc.count {
					t.Fatal(got, err)
				}
			})
		}
	}
}

func TestNullWorkloadAuthorityIsInvalid(t *testing.T) {
	body := `{"enabled":true,"role":"developer","providers":{"allowed":["codex"],"target":"codex"},"resolved_role_ceiling":{"configured":false},"workload_class_recommendation":null}`
	if _, err := decodePreflight([]byte(body)); err == nil {
		t.Fatal("null workload authority accepted")
	}
}
