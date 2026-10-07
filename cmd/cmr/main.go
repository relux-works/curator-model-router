package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Refusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Refusal) Error() string { return e.Code + ": " + e.Message }
func main()                      { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runHelp(nil, stdout, stderr)
	}
	switch args[0] {
	case "recommend":
		return runRecommend(args[1:], stdout, stderr)
	case "usage":
		return runUsage(args[1:], stdout, stderr)
	case "spawn":
		return runSpawn(args[1:], stdout, stderr)
	case "route":
		return runRoute(args[1:], stdout, stderr)
	case "explain":
		return runExplain(args[1:], stdout, stderr)
	case "replay":
		return runReplay(args[1:], stdout, stderr)
	case "headroom":
		return runHeadroom(args[1:], stdout, stderr)
	case "evidence":
		return runEvidence(args[1:], stdout, stderr)
	case "note":
		return runNote(args[1:], stdout, stderr)
	case "suitability":
		return runSuitability(args[1:], stdout, stderr)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		return runHelp(args[1:], stdout, stderr)
	default:
		return writeRefusal(&Refusal{Code: "cmr_unknown_subcommand", Message: "unknown subcommand: " + strings.ReplaceAll(strings.ReplaceAll(args[0], "\n", " "), "\r", " ")}, hasJSON(args), stdout, stderr)
	}
}
func hasJSON(args []string) bool {
	// This is also used on invalid-input paths. Known value options still consume
	// a token, so a value named --json never changes the output channel.
	valueFlags := strings.Fields("role task-class difficulty language agent model reasoning-effort catalog policy candidates platform host story producer-family exclude mode store registry importer imported-at mapping runtime ttl input decision snapshot derivation project-reqs evaluated-at reason author kind scope text id file efforts harness-version-range statement polarity categories languages platforms roles basis confidence author-kind created-at review-by supersedes evidence-ref at")
	asJSON := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		key, value, inline := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !strings.HasPrefix(args[i], "-") {
			continue
		}
		if key == "json" {
			asJSON = true
			if inline {
				asJSON, _ = strconv.ParseBool(value)
			}
			continue
		}
		for _, name := range valueFlags {
			if key == name && !inline {
				i++
				break
			}
		}
	}
	return asJSON
}

func writeRefusal(err *Refusal, asJSON bool, stdout, stderr io.Writer) int {
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			Error *Refusal `json:"error"`
		}{err})
	} else {
		_, _ = fmt.Fprintln(stderr, err.Error())
	}
	return 2
}
