package cmrio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"time"

	"github.com/relux-works/curator-model-router/pkg/recommend"
	"github.com/relux-works/skill-agents-management/pkg/providerquota"
)

var queryName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func admissionError(message string) error {
	return &recommend.Refusal{Code: "invalid_admission", Message: message}
}

// Discover never falls back after task-board is found: an empty or broken
// authority must not be widened to the PATH catalog.
func Discover(file, role, agent string, catalog recommend.Catalog, contexts ...*recommend.AdmissionContext) ([]recommend.Candidate, string, error) {
	var admission *recommend.AdmissionContext
	if len(contexts) != 0 {
		admission = contexts[0]
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, "", admissionError("cannot read candidates")
		}
		out, err := recommend.LoadCandidates(b)
		if err != nil {
			return nil, "", admissionError("invalid candidates: " + err.Error())
		}
		return out, "candidates-file", nil
	}
	binary, err := exec.LookPath("task-board")
	if err != nil {
		out := []recommend.Candidate{}
		for _, row := range catalog.Rows {
			// Quota resolution protects credential roots, including symlink targets.
			_, err := providerquota.ResolveBinary(providerquota.Request{Env: os.Environ(), ForbiddenRoots: []string{os.Getenv("HOME") + "/.curator"}}, providerquota.BinarySpec{Name: row.Runtime}, providerquota.NativeBinaryFS{})
			if err == nil {
				out = append(out, row.Candidate)
			}
		}
		return out, "path-catalog", nil
	}
	if !queryName.MatchString(role) || agent != "" && !queryName.MatchString(agent) {
		return nil, "", admissionError("unsafe role or agent query value")
	}
	query := func(a string) (preflight, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		q := "project_config(view=spawn-preflight, role=" + role
		if a != "" {
			q += ", agent=" + a
		}
		q += ")"
		argv := []string{"--no-update-check"}
		if admission != nil {
			argv = append(argv, admission.BoardFlags...)
		}
		argv = append(argv, "q", q)
		cmd := exec.CommandContext(ctx, binary, argv...)
		cmd.WaitDelay = time.Second
		var b limitedBuffer
		cmd.Stdout = &b
		cmd.Stderr = io.Discard
		if cmd.Run() != nil {
			return preflight{}, admissionError("spawn-preflight failed")
		}
		return decodePreflight(b.Bytes())
	}
	first, err := query(agent)
	if err != nil {
		return nil, "", err
	}
	agents := first.Providers.Allowed
	if agent != "" {
		if !slices.Contains(agents, agent) {
			return []recommend.Candidate{}, "spawn-preflight", nil
		}
		agents = []string{agent}
	}
	out := []recommend.Candidate{}
	for _, a := range agents {
		if !queryName.MatchString(a) {
			return nil, "", admissionError("unsafe allowed agent query value")
		}
		p := first
		if agent == "" || a != first.Providers.Target {
			p, err = query(a)
			if err != nil {
				return nil, "", err
			}
		}
		if p.Providers.Target != a || p.Role != role {
			return nil, "", admissionError("spawn-preflight target mismatch")
		}
		pairs, err := p.candidates(a, catalog)
		if err != nil {
			return nil, "", err
		}
		if admission != nil {
			required, err := p.rationaleRequired()
			if err != nil {
				return nil, "", err
			}
			if admission.RationaleRequired == nil {
				admission.RationaleRequired = map[string]bool{}
			}
			admission.RationaleRequired[a] = required
		}
		out = append(out, pairs...)
	}
	return out, "spawn-preflight", nil
}

type pair struct {
	Runtime string `json:"runtime"`
	Model   string `json:"model"`
	Effort  string `json:"reasoning_effort"`
}
type preflight struct {
	workloadFields map[string]json.RawMessage
	Enabled        *bool  `json:"enabled"`
	Role           string `json:"role"`
	Providers      struct {
		Allowed []string `json:"allowed"`
		Target  string   `json:"target"`
	} `json:"providers"`
	Ceiling *struct {
		ContractVersion        string `json:"contract_version"`
		ModelCriterion         string `json:"model_criterion"`
		AdjustmentConfirmation string `json:"adjustment_confirmation"`
		Configured             *bool  `json:"configured"`
		MigrationRequired      bool   `json:"migration_required"`
		Admitted               *struct {
			Provider string `json:"provider"`
			Models   []struct {
				ID      string   `json:"id"`
				Efforts []string `json:"efforts"`
			} `json:"models"`
		} `json:"admitted_pairs"`
	} `json:"resolved_role_ceiling"`
	Workload *struct {
		Configured    *bool   `json:"configured"`
		ClassResolved *bool   `json:"class_resolved"`
		Available     *[]pair `json:"available_pairs"`
		Integrity     *struct {
			Determinate *bool `json:"determinate"`
		} `json:"limit_read_integrity"`
		Unresolved string `json:"unresolved_reason"`
	} `json:"workload_class_recommendation"`
}

func decodePreflight(b []byte) (preflight, error) {
	// q emits a one-operation array; accept the direct object for adapters too.
	var operations []json.RawMessage
	if len(bytes.TrimSpace(b)) > 0 && bytes.TrimSpace(b)[0] == '[' {
		if json.Unmarshal(b, &operations) != nil || len(operations) != 1 {
			return preflight{}, admissionError("invalid query operation response")
		}
		b = operations[0]
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil {
		return preflight{}, admissionError("invalid spawn-preflight JSON")
	}
	if raw, present := fields["workload_class_recommendation"]; present && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return preflight{}, admissionError("null workload authority")
	}
	var p preflight
	if json.Unmarshal(b, &p) != nil || p.Enabled == nil || p.Providers.Allowed == nil || p.Providers.Target == "" || p.Ceiling == nil || p.Ceiling.Configured == nil {
		return p, admissionError("invalid spawn-preflight JSON")
	}
	if p.Workload != nil {
		if json.Unmarshal(fields["workload_class_recommendation"], &p.workloadFields) != nil {
			return p, admissionError("invalid workload authority")
		}
	}
	return p, nil
}
func (p preflight) candidates(agent string, catalog recommend.Catalog) ([]recommend.Candidate, error) {
	out := []recommend.Candidate{}
	if !*p.Enabled || !slices.Contains(p.Providers.Allowed, agent) {
		return out, nil
	}
	if p.Ceiling.MigrationRequired {
		return nil, admissionError("role ceiling requires migration")
	}
	admitted := map[string]bool{}
	if *p.Ceiling.Configured {
		if p.Ceiling.Admitted == nil || p.Ceiling.Admitted.Provider != agent || p.Ceiling.Admitted.Models == nil {
			return nil, admissionError("missing canonical role admission")
		}
		for _, m := range p.Ceiling.Admitted.Models {
			for _, effort := range m.Efforts {
				admitted[recommend.Candidate{Runtime: agent, Model: m.ID, Effort: boardEffort(effort)}.Key()] = true
			}
		}
	}
	if p.Workload != nil {
		w := p.Workload
		_, availablePresent := p.workloadFields["available_pairs"]
		_, integrityPresent := p.workloadFields["limit_read_integrity"]
		_, classPresent := p.workloadFields["class_resolved"]
		roleOnly := w.Configured != nil && *w.Configured && w.ClassResolved != nil && !*w.ClassResolved && w.Unresolved == "workload_class_derivation_input_required" && !availablePresent && !integrityPresent
		unconfigured := w.Configured != nil && !*w.Configured && !classPresent && w.Unresolved == "" && !availablePresent && !integrityPresent
		if !roleOnly && !unconfigured {
			if w.Configured == nil || !*w.Configured || w.ClassResolved == nil || !*w.ClassResolved || w.Unresolved != "" || w.Integrity == nil || w.Integrity.Determinate == nil || !*w.Integrity.Determinate || w.Available == nil {
				return nil, admissionError("missing or indeterminate workload availability")
			}
			for _, x := range *w.Available {
				c := recommend.Candidate{Runtime: x.Runtime, Model: x.Model, Effort: boardEffort(x.Effort)}
				if c.Runtime == agent && (!*p.Ceiling.Configured || admitted[c.Key()]) {
					out = append(out, c)
				}
			}
			return out, nil
		}
	} else if *p.Ceiling.Configured {
		return nil, admissionError("missing workload authority for configured ceiling")
	}
	// Only explicit role-only/unconfigured workload states permit fallback.
	if *p.Ceiling.Configured {
		for _, m := range p.Ceiling.Admitted.Models {
			for _, e := range m.Efforts {
				out = append(out, recommend.Candidate{Runtime: agent, Model: m.ID, Effort: boardEffort(e)})
			}
		}
		return out, nil
	}
	// No configured role ceiling means task-board admits any model and effort
	// of an allowed provider, so every catalog row of that runtime is admitted.
	// The provider allow-set above still applies; nothing beyond it is widened.
	for _, row := range catalog.Rows {
		if row.Candidate.Runtime == agent {
			out = append(out, row.Candidate)
		}
	}
	return out, nil
}

// Derived from origin/main selection_policy.go's confirmation validator.
func (p preflight) rationaleRequired() (bool, error) {
	c := p.Ceiling
	if !*c.Configured {
		return false, nil
	}
	switch c.ContractVersion {
	case "spawn-policy-v3", "spawn-policy-v4":
		switch c.AdjustmentConfirmation {
		case "":
			return false, nil // provider has no effective allow-set
		case "none":
			return false, nil
		case "required":
			return true, nil
		default:
			return false, admissionError("invalid adjustment confirmation")
		}
	case "", "spawn-policy-v2":
		switch c.ModelCriterion {
		case "", "equal":
			return false, nil
		case "less_or_equal", "greater_or_equal":
			return true, nil
		default:
			return false, admissionError("invalid model criterion")
		}
	default:
		return false, admissionError("unsupported ceiling contract")
	}
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, admissionError("preflight output too large")
	}
	return b.Buffer.Write(p)
}

// task-board represents a model without an effort axis as an empty effort.
// The catalog spells that same configuration "none"; no other value is inferred.
func boardEffort(e string) string {
	if e == "" {
		return "none"
	}
	return e
}
