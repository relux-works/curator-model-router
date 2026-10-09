package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

// Local tooling reads only explicit input files and writes only to stdout.
func runLocal(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "usage: cmr local providers | import-base --input FILE [--provider public-json] | coefficients --input FILE | decide --input FILE"}, hasJSON(args), stdout, stderr)
	}
	if args[0] == "providers" {
		if len(args) != 1 {
			return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "providers takes no flags"}, hasJSON(args), stdout, stderr)
		}
		if json.NewEncoder(stdout).Encode(recommend.DefaultBaseProviders().List()) != nil {
			return localOutputError(stdout, stderr)
		}
		return 0
	}
	if args[0] != "import-base" && args[0] != "coefficients" && args[0] != "decide" {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "unknown local subcommand"}, false, stdout, stderr)
	}
	f := flag.NewFlagSet("local", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	input := f.String("input", "", "")
	provider := "public-json"
	if args[0] == "import-base" {
		f.StringVar(&provider, "provider", "public-json", "")
	}
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 || *input == "" {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "local command requires --input FILE"}, false, stdout, stderr)
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_local_input_failed", Message: "cannot read local input"}, false, stdout, stderr)
	}
	var out any
	switch args[0] {
	case "import-base":
		out, err = recommend.DefaultBaseProviders().Import(provider, raw)
	case "coefficients":
		out, err = recommend.LoadCoefficientTable(raw)
	case "decide":
		var in recommend.Request
		in, err = recommend.LoadLocalRequest(raw)
		if err == nil {
			out, err = recommend.BuildDecision(in)
		}
	default:
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "unknown local subcommand"}, false, stdout, stderr)
	}
	if err != nil {
		return commandError(err, false, stdout, stderr)
	}
	if json.NewEncoder(stdout).Encode(out) != nil {
		return localOutputError(stdout, stderr)
	}
	if d, ok := out.(recommend.DecisionRecord); ok && d.Recommendation.Refusal != nil {
		return 2
	}
	return 0
}
func localOutputError(stdout, stderr io.Writer) int {
	return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "cannot write local output"}, false, stdout, stderr)
}
