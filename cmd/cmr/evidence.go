package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/evidence"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type evidenceFlags struct {
	store, registry string
	json            bool
}

func addEvidenceFlags(f *flag.FlagSet, c *evidenceFlags) {
	f.StringVar(&c.store, "store", "", "evidence store directory")
	f.StringVar(&c.registry, "registry", "", "frozen registry JSON")
	f.BoolVar(&c.json, "json", false, "JSON output")
}

// parseEvidenceFlags accepts flags before or after positionals, as the brief's
// cmr evidence import FILE --store DIR examples require.
func parseEvidenceFlags(f *flag.FlagSet, args []string) error {
	f.SetOutput(io.Discard)
	options := []string{}
	positions := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positions = append(positions, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(a, "-")
			name, _, inline := strings.Cut(name, "=")
			x := f.Lookup(name)
			if x == nil {
				return &Refusal{"cmr_invalid_arguments", "unknown flag: " + name}
			}
			options = append(options, a)
			boolean := false
			if b, ok := x.Value.(interface{ IsBoolFlag() bool }); ok {
				boolean = b.IsBoolFlag()
			}
			if !boolean && !inline {
				if i+1 >= len(args) {
					return &Refusal{"cmr_invalid_arguments", "missing flag value"}
				}
				i++
				options = append(options, args[i])
			}
		} else {
			positions = append(positions, a)
		}
	}
	options = append(options, "--")
	options = append(options, positions...)
	if err := f.Parse(options); err != nil {
		return &Refusal{"cmr_invalid_arguments", "invalid flags"}
	}
	return nil
}
func evidenceRefusal(err error, args []string, out, stderr io.Writer) int {
	var r *Refusal
	if errors.As(err, &r) {
		return writeRefusal(r, hasJSON(args), out, stderr)
	}
	var e *evidence.Error
	if errors.As(err, &e) {
		return writeRefusal(&Refusal{e.Code, e.Message}, hasJSON(args), out, stderr)
	}
	var c *canonical.Error
	if errors.As(err, &c) {
		return writeRefusal(&Refusal{c.Code, c.Message}, hasJSON(args), out, stderr)
	}
	return writeRefusal(&Refusal{"cmr_evidence_failed", "evidence operation failed"}, hasJSON(args), out, stderr)
}
func evidenceOutput(value any, asJSON bool, out, stderr io.Writer) int {
	var err error
	if asJSON {
		err = json.NewEncoder(out).Encode(value)
	} else {
		var b []byte
		b, err = json.MarshalIndent(value, "", "  ")
		if err == nil {
			_, err = fmt.Fprintln(out, string(b))
		}
	}
	if err != nil {
		return writeRefusal(&Refusal{"cmr_output_failed", "could not write evidence output"}, asJSON, out, stderr)
	}
	return 0
}
func loadEvidenceStore(c evidenceFlags) (*evidence.Store, evidence.Registry, error) {
	r, err := evidence.PinnedRegistry()
	if err != nil {
		return nil, r, err
	}
	if c.registry != "" {
		b, e := os.ReadFile(c.registry)
		if e != nil {
			return nil, r, &Refusal{"cmr_invalid_arguments", "could not read registry file"}
		}
		if e = evidence.Decode(b, &r); e != nil {
			return nil, r, e
		}
	}
	root := c.store
	if root == "" {
		// TODO(decision): prefer XDG_DATA_HOME/curator/model-router/evidence;
		// otherwise use the same layout under the user's local share directory.
		// This keeps data separate from Curator configuration and authentication.
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return nil, r, &Refusal{"cmr_invalid_arguments", "cannot determine data root; pass --store"}
			}
			base = filepath.Join(home, ".local", "share")
		}
		root = filepath.Join(base, "curator", "model-router", "evidence")
	}
	s, err := evidence.NewStore(root, r)
	return s, r, err
}
func runEvidence(args []string, out, stderr io.Writer) int {
	if isEvalRunCommand(args) {
		return runEvalRun(args[1:], out, stderr)
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "usage: cmr evidence import FILE | ls | show ID | unresolved | rebind --registry FILE [--json] [--store DIR]"}, hasJSON(args), out, stderr)
	}
	f := flag.NewFlagSet("evidence", flag.ContinueOnError)
	c := evidenceFlags{}
	addEvidenceFlags(f, &c)
	importer := f.String("importer", "native", "native or bughunt")
	at := f.String("imported-at", "", "explicit RFC3339 import time for bughunt")
	if err := parseEvidenceFlags(f, args[1:]); err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	action := args[0]
	count := 0
	if action == "import" || action == "show" {
		count = 1
	}
	if !strings.Contains("|import|ls|show|unresolved|rebind|", "|"+action+"|") {
		return writeRefusal(&Refusal{"cmr_unknown_subcommand", "unknown evidence subcommand"}, c.json, out, stderr)
	}
	if f.NArg() != count {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "invalid evidence positional arguments"}, c.json, out, stderr)
	}
	if action == "rebind" && c.registry == "" {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "rebind requires --registry FILE"}, c.json, out, stderr)
	}
	s, r, err := loadEvidenceStore(c)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	var value any
	switch action {
	case "import":
		b, e := os.ReadFile(f.Arg(0))
		if e != nil {
			return writeRefusal(&Refusal{"cmr_invalid_arguments", "could not read import file"}, c.json, out, stderr)
		}
		var doc evidence.Import
		switch *importer {
		case "native":
			doc, _, err = evidence.Native(b, r)
		case "bughunt":
			doc, _, err = evidence.BugHunt(b, *at)
		default:
			err = &Refusal{"cmr_invalid_arguments", "importer must be native or bughunt"}
		}
		if err == nil {
			var result evidence.ImportResult
			result, err = s.Add(doc)
			value = result
			if *importer == "bughunt" {
				// Retrieval time is pinned independently of --imported-at.
				var export evidence.BugHuntExport
				if e := evidence.Decode(b, &export); e != nil {
					err = e
				} else {
					value = struct {
						evidence.ImportResult
						RetrievedAt string `json:"retrieved_at"`
					}{result, export.RetrievedAt}
				}
			}
		}
	case "rebind":
		value, err = s.Rebind()
	case "ls":
		var snap evidence.Snapshot
		var digest string
		var active evidence.ActiveSet
		snap, digest, _, active, err = s.Inspect()
		value = struct {
			Snapshot evidence.Snapshot  `json:"snapshot"`
			Digest   string             `json:"digest"`
			Active   evidence.ActiveSet `json:"active_set"`
		}{snap, digest, active}
	case "show":
		value, err = s.Show(f.Arg(0))
	case "unresolved":
		value, err = s.Unresolved()
	}
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	return evidenceOutput(value, c.json, out, stderr)
}
