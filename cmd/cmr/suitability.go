package main

import (
	"flag"
	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/evidence"
	"io"
	"os"
	"time"
)

func runSuitability(args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("suitability", flag.ContinueOnError)
	c := evidenceFlags{}
	addEvidenceFlags(f, &c)
	role := f.String("role", "", "playbook role")
	project := f.String("project-reqs", "", "RequirementsInput JSON")
	candidateFile := f.String("candidates", "", "versioned candidate fingerprint document")
	evaluated := f.String("evaluated-at", "", "frozen RFC3339 time")
	derivationFile := f.String("derivation", "", "versioned Derivation JSON (default version 1)")
	if err := parseEvidenceFlags(f, args); err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	if f.NArg() != 0 || *role == "" {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "usage: cmr suitability --role ROLE [--project-reqs FILE] [--evaluated-at RFC3339] [--candidates FILE] [--derivation FILE] [--store DIR] [--json]"}, c.json, out, stderr)
	}
	s, r, err := loadEvidenceStore(c)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	req, err := evidence.RoleRequirements(*role, "default")
	if err != nil && *project == "" {
		return evidenceRefusal(err, args, out, stderr)
	}
	if *project != "" {
		b, e := os.ReadFile(*project)
		if e != nil {
			return writeRefusal(&Refusal{"cmr_invalid_arguments", "could not read requirements file"}, c.json, out, stderr)
		}
		if err = evidence.Decode(b, &req); err != nil {
			return evidenceRefusal(err, args, out, stderr)
		}
		if req.Role != *role {
			return writeRefusal(&Refusal{"cmr_invalid_arguments", "project requirements role differs from --role"}, c.json, out, stderr)
		}
	}
	snap, _, docs, _, err := s.Inspect()
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	r, err = s.RegistryFor(snap)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	candidates := evidence.EvidenceCandidates(docs, r)
	if *candidateFile != "" {
		b, e := os.ReadFile(*candidateFile)
		if e != nil {
			return writeRefusal(&Refusal{"cmr_invalid_arguments", "could not read candidate file"}, c.json, out, stderr)
		}
		var input struct {
			SchemaVersion string                 `json:"schema_version"`
			Candidates    []evidence.Fingerprint `json:"candidates"`
		}
		if err = evidence.Decode(b, &input); err != nil {
			return evidenceRefusal(err, args, out, stderr)
		}
		if input.SchemaVersion != evidence.SchemaVersion {
			return writeRefusal(&Refusal{"cmr_invalid_arguments", "unsupported candidate schema"}, c.json, out, stderr)
		}
		candidates = input.Candidates
	}
	canonical.SortByKey(candidates, func(x evidence.Fingerprint) string {
		b, _ := canonical.Marshal(struct {
			SchemaVersion string               `json:"schema_version"`
			Fingerprint   evidence.Fingerprint `json:"fingerprint"`
		}{evidence.SchemaVersion, x})
		return string(b)
	})
	at := *evaluated
	if at == "" {
		at = time.Now().UTC().Format(time.RFC3339Nano)
	}
	derivation, err := loadDerivation(*derivationFile)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	view, err := evidence.Derive(snap, docs, r, req, derivation, candidates, at)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	if err = s.SaveView(view); err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	return evidenceOutput(view, c.json, out, stderr)
}
