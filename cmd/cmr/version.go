package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
)

// readBuildInfo is replaced in tests.
var readBuildInfo = debug.ReadBuildInfo

// buildVersion reports the module version stamped by the Go toolchain, e.g.
// v0.3.3 for `go install ...@v0.3.3`. A local build from a checkout reports
// devel, with the VCS revision when the toolchain recorded one.
func buildVersion() string {
	info, ok := readBuildInfo()
	if !ok || info == nil {
		return "devel"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return "devel"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += "-dirty"
	}
	return "devel+" + revision
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--json") {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "usage: cmr version [--json]"}, hasJSON(args), stdout, stderr)
	}
	version := buildVersion()
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
