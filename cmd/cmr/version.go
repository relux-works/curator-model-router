package main

import (
	"encoding/json"
	"fmt"
	"io"
)

const version = "0.0.0-w0"

func runVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--json") {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "usage: cmr version [--json]"}, hasJSON(args), stdout, stderr)
	}
	var err error
	if hasJSON(args) {
		err = json.NewEncoder(stdout).Encode(struct {
			Version string `json:"version"`
		}{version})
	} else {
		_, err = fmt.Fprintln(stdout, "cmr "+version)
	}
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write version"}, hasJSON(args), stdout, stderr)
	}
	return 0
}
