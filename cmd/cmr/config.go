package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "show" {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "usage: cmr config show [--policy FILE] [--catalog FILE] [--json]"}, hasJSON(args), stdout, stderr)
	}
	f := flag.NewFlagSet("config show", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	asJSON := f.Bool("json", false, "JSON output")
	policy := f.String("policy", "", "explicit policy path")
	catalog := f.String("catalog", "", "explicit catalog path")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "invalid config show flags"}, hasJSON(args), stdout, stderr)
	}
	paths := map[string]cmrio.ConfigSource{}
	for _, kind := range []string{"policy", "catalog"} {
		explicit := *policy
		if kind == "catalog" {
			explicit = *catalog
		}
		source, err := cmrio.ResolveConfig(kind, explicit)
		if err != nil {
			return commandError(err, *asJSON, stdout, stderr)
		}
		paths[kind] = source
	}
	for kind, path := range map[string]string{"state_root": cmrio.StateRoot(), "data_root": cmrio.DataRoot()} {
		_, err := os.Stat(path)
		if err != nil && !os.IsNotExist(err) {
			return writeRefusal(&Refusal{Code: "cmr_config_read_failed", Message: "cannot inspect " + kind}, *asJSON, stdout, stderr)
		}
		paths[kind] = cmrio.ConfigSource{Path: path, Layer: "user", Candidates: []cmrio.PathCandidate{{Path: path, Layer: "user", Exists: err == nil}}}
	}
	var err error
	if *asJSON {
		err = json.NewEncoder(stdout).Encode(paths)
	} else {
		for _, kind := range []string{"policy", "catalog", "state_root", "data_root"} {
			source := paths[kind]
			path := source.Path
			if source.Layer == "default" {
				path = "(built-in)"
			}
			if _, err = fmt.Fprintf(stdout, "%s: %s (%s)\n", kind, path, source.Layer); err != nil {
				break
			}
			for _, c := range source.Candidates {
				status := "missing"
				if c.Exists {
					status = "exists"
				}
				if _, err = fmt.Fprintf(stdout, "  %s %s: %s\n", c.Layer, status, c.Path); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write config output"}, *asJSON, stdout, stderr)
	}
	return 0
}
func runMigrate(args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("migrate", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	dryRun := f.Bool("dry-run", false, "report without writing")
	asJSON := f.Bool("json", false, "JSON output")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "usage: cmr migrate [--dry-run] [--json]"}, hasJSON(args), stdout, stderr)
	}
	report, migrateErr := cmrio.Migrate(*dryRun)
	var refusal *Refusal
	if migrateErr != nil {
		r := migrateErr.(*recommend.Refusal)
		refusal = &Refusal{Code: r.Code, Message: r.Message}
	}
	var err error
	if *asJSON {
		err = json.NewEncoder(stdout).Encode(struct {
			cmrio.MigrationReport
			Error *Refusal `json:"error,omitempty"`
		}{report, refusal})
	} else {
		for _, entry := range report.Entries {
			if _, err = fmt.Fprintf(stdout, "%s: %s -> %s\n", entry.Status, entry.Source, entry.Destination); err != nil {
				break
			}
		}
	}
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write migration output"}, *asJSON, stdout, stderr)
	}
	if refusal != nil {
		if !*asJSON {
			return writeRefusal(refusal, false, stdout, stderr)
		}
		return 2
	}
	return 0
}
