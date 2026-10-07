package main

import (
	"flag"
	"io"
	"os"

	"github.com/relux-works/curator-model-router/pkg/evidence"
)

func runEvalRun(args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("import-evalrun", flag.ContinueOnError)
	c := evidenceFlags{}
	addEvidenceFlags(f, &c)
	mapping := f.String("mapping", "", "versioned evalrun mapping JSON")
	importer := f.String("importer", "evalrun", "evalrun")
	at := f.String("imported-at", "", "not accepted for evalrun")
	if err := parseEvidenceFlags(f, args); err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	suppliedAt := false
	f.Visit(func(x *flag.Flag) {
		if x.Name == "imported-at" {
			suppliedAt = true
		}
	})
	if f.NArg() != 1 || *mapping == "" || *importer != "evalrun" || suppliedAt || *at != "" {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "usage: cmr evidence import-evalrun FILE --mapping FILE [--registry FILE] [--store DIR] [--json]; export supplies import time"}, c.json, out, stderr)
	}
	raw, err := os.ReadFile(f.Arg(0))
	if err != nil {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "could not read evalrun file"}, c.json, out, stderr)
	}
	mb, err := os.ReadFile(*mapping)
	if err != nil {
		return writeRefusal(&Refusal{"cmr_invalid_arguments", "could not read mapping file"}, c.json, out, stderr)
	}
	var m evidence.EvalRunMapping
	if err = evidence.Decode(mb, &m); err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	s, _, err := loadEvidenceStore(c)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	result, err := s.AddEvalRun(raw, m)
	if err != nil {
		return evidenceRefusal(err, args, out, stderr)
	}
	return evidenceOutput(result, c.json, out, stderr)
}

// loadDerivation preserves the historical default and accepts only strict data.
func loadDerivation(path string) (evidence.Derivation, error) {
	d := evidence.DefaultDerivation()
	if path == "" {
		return d, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return d, &Refusal{"cmr_invalid_arguments", "could not read derivation file"}
	}
	if err = evidence.Decode(b, &d); err != nil {
		return d, err
	}
	return d, d.Validate()
}

func isEvalRunCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "import-evalrun" {
		return true
	}
	if args[0] != "import" {
		return false
	}
	f := flag.NewFlagSet("import-dispatch", flag.ContinueOnError)
	var c evidenceFlags
	addEvidenceFlags(f, &c)
	importer := f.String("importer", "native", "")
	f.String("mapping", "", "")
	f.String("imported-at", "", "")
	if parseEvidenceFlags(f, args[1:]) != nil {
		return false
	}
	return *importer == "evalrun"
}
