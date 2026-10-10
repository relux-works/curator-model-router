package main

import (
	"encoding/json"
	"io"
)

func runHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--json") {
		return writeRefusal(&Refusal{Code: "cmr_invalid_arguments", Message: "usage: cmr help [--json]"}, hasJSON(args), stdout, stderr)
	}
	var err error
	if hasJSON(args) {
		err = json.NewEncoder(stdout).Encode(struct {
			Commands []string `json:"commands"`
		}{[]string{"help", "version", "route", "explain", "replay", "headroom explain", "evidence", "note", "suitability", "recommend", "local", "usage", "spawn", "shadow report"}})
	} else {
		_, err = io.WriteString(stdout, "Usage: cmr <command> [--json]\n\nCommands:\n  help              Show help\n  version           Show version\n  route             Route a frozen input bundle\n  explain           Explain a recorded decision\n  replay            Replay a recorded decision\n  headroom explain  Explain a usage snapshot\n  evidence          Import and inspect evidence (import, import-evalrun FILE --mapping FILE, ls, show, unresolved, rebind)\n  note              Add or retract an internal note\n  suitability       Derive the role-suitability view\n  local             Import public base scores, validate coefficients, or decide offline\n  recommend         Select a task-aware launch configuration\n  usage             Refresh or show cached provider usage\n  spawn             Recommend and invoke task-board spawn\n  shadow report     Summarize shadow spawn observations\n")
	}
	if err != nil {
		return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write help"}, hasJSON(args), stdout, stderr)
	}
	return 0
}
