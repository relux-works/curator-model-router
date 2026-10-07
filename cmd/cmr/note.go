package main

import (
	"flag"
	"github.com/relux-works/curator-model-router/pkg/evidence"
	"io"
	"os"
	"strings"
	"time"
)

func csv(raw string) []string {
	if raw == "" {
		return []string{}
	}
	return strings.Split(raw, ",")
}
func runNote(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "usage: cmr note add ... | retract ID --reason TEXT --author ID"}, hasJSON(args), out, stderr)
	}
	action := args[0]
	f := flag.NewFlagSet("note", flag.ContinueOnError)
	c := evidenceFlags{}
	addEvidenceFlags(f, &c)
	file := f.String("file", "", "note document with schema_version and note")
	model := f.String("model", "", "registry model id")
	runtime := f.String("runtime", "", "runtime id")
	efforts := f.String("efforts", "", "comma-separated exact efforts")
	harness := f.String("harness-version-range", "", "exact version or inclusive [a,b]")
	statement := f.String("statement", "", "short claim")
	polarity := f.String("polarity", "strength", "strength, weakness or caution")
	categories := f.String("categories", "", "comma-separated task categories")
	languages := f.String("languages", "", "comma-separated languages")
	platforms := f.String("platforms", "", "comma-separated platforms")
	roles := f.String("roles", "", "comma-separated role aliases")
	basis := f.String("basis", "operator-judgement", "note basis")
	confidence := f.String("confidence", "medium", "low, medium or high")
	author := f.String("author", "", "opaque author id")
	authorKind := f.String("author-kind", "human", "human or agent")
	created := f.String("created-at", "", "RFC3339")
	review := f.String("review-by", "", "date or RFC3339")
	supersedes := f.String("supersedes", "", "comma-separated record addresses")
	refs := f.String("evidence-ref", "", "document reference")
	reason := f.String("reason", "", "reason for retraction")
	at := f.String("at", "", "RFC3339 import/retraction time")
	if err := parseEvidenceFlags(f, args[1:]); err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	if action != "add" && action != "retract" {
		return writeRefusal(&Refusal{"cmr_unknown_subcommand", "unknown note subcommand"}, c.json, out, stderr)
	}
	if action == "add" && f.NArg() != 0 || action == "retract" && f.NArg() != 1 {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "invalid note positional arguments"}, c.json, out, stderr)
	}
	s, r, err := loadEvidenceStore(c)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	when := *at
	if when == "" {
		when = time.Now().UTC().Format(time.RFC3339Nano)
	}
	var doc evidence.Import
	createdAt := ""
	if action == "retract" {
		doc, _, err = evidence.RetractionImport(evidence.Retraction{Target: f.Arg(0), Reason: *reason, Author: evidence.Author{Kind: *authorKind, ID: *author}, At: when}, when, r)
	} else {
		n := evidence.Note{Subject: evidence.NoteSubject{ModelID: *model, Runtime: *runtime, Efforts: csv(*efforts), HarnessVersionRange: *harness}, Claim: evidence.Claim{Polarity: *polarity, Statement: *statement, Categories: csv(*categories)}, Basis: *basis, EvidenceRefs: []evidence.EvidenceRef{}, Confidence: *confidence, Author: evidence.Author{Kind: *authorKind, ID: *author}, CreatedAt: *created, ReviewBy: *review, Supersedes: csv(*supersedes)}
		if n.CreatedAt == "" {
			n.CreatedAt = when
		}
		if *refs != "" {
			n.EvidenceRefs = append(n.EvidenceRefs, evidence.EvidenceRef{Kind: "document", Ref: *refs})
		}
		if *languages != "" || *platforms != "" || *roles != "" {
			n.Claim.Facets = &evidence.Facets{Languages: csv(*languages), Platforms: csv(*platforms), Roles: csv(*roles)}
		}
		if *file != "" {
			b, e := os.ReadFile(*file)
			if e != nil {
				return writeRefusal(&Refusal{"cmr_invalid_arguments", "could not read note file"}, c.json, out, stderr)
			}
			var envelope struct {
				SchemaVersion string        `json:"schema_version"`
				Note          evidence.Note `json:"note"`
			}
			if err = evidence.Decode(b, &envelope); err != nil {
				return evidenceRefusal(err, args, out, stderr)
			}
			if envelope.SchemaVersion != evidence.SchemaVersion {
				return writeRefusal(&Refusal{"cmr_invalid_arguments", "unsupported note schema"}, c.json, out, stderr)
			}
			n = envelope.Note
		}
		createdAt = n.CreatedAt
		doc, _, err = evidence.NoteImport(n, when, r)
	}
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	value, err := s.Add(doc)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	return evidenceOutput(struct {
		evidence.ImportResult
		CreatedAt string `json:"created_at,omitempty"`
		At        string `json:"at"`
	}{value, createdAt, when}, c.json, out, stderr)
}
