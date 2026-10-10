package main

import (
	"strconv"
	"strings"
)

// Arity from skill-project-management origin/main:tools/board-cli/cmd/spawn.go
// (inspected 2026-10-04). true consumes one value, false is a boolean whose
// optional value must use '='. Include Cobra's help and root.go inherited flags.
// Unknown flags refuse: guessing their arity could change the launch selection.
var taskBoardSpawnFlags = map[string]bool{
	"agent": true, "model": true, "reasoning-effort": true,
	"selection-rationale": true, "ack-resolved": true,
	"workload-class": true, "workload-task-class": true,
	"recommendation-rationale": true, "recommendation-snapshot-digest": true,
	"task-path": true, "dod-path": true, "instruction": true, "role": true,
	"background": false, "allow-parallel": false, "wait": false, "verbose": false,
	"timeout": true, "hard-timeout": true, "deadline-multiplier": true,
	"budget": true, "goal-scope": true, "context": true,
	"no-context": false, "register-context": true, "help": false,
	"board-dir": true, "json": false, "no-update-check": false,
	"remote": true, "remote-board": true, "insecure": false, "context-profile": true,
}

type spawnArguments struct {
	boardFlags       []string
	rationalePresent bool
	allowParallel    bool // the caller already passed --allow-parallel (any value)
	locks            map[string]string
	injectionIndex   int // end of argv or an actual, unconsumed -- terminator
}

func parseSpawnArgs(args []string) (spawnArguments, error) {
	p := spawnArguments{locks: map[string]string{}, injectionIndex: len(args)}
	invalid := func(message string) (spawnArguments, error) {
		return p, &Refusal{Code: "cmr_invalid_arguments", Message: message}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.injectionIndex = i
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			continue // pflag allows interspersed positionals and options
		}
		if !strings.HasPrefix(a, "--") {
			// Both registered shorthands are booleans. pflag permits clusters.
			short, value, equals := strings.Cut(a[1:], "=")
			if short == "" {
				return invalid("empty task-board spawn shorthand")
			}
			for _, c := range short {
				if c != 'v' && c != 'h' {
					return invalid("unknown task-board spawn shorthand: -" + string(c))
				}
			}
			if equals {
				if _, err := strconv.ParseBool(value); err != nil {
					return invalid("invalid task-board boolean: " + a)
				}
			}
			continue
		}
		key, value, equals := strings.Cut(a[2:], "=")
		takesValue, known := taskBoardSpawnFlags[key]
		if !known {
			return invalid("unknown task-board spawn flag: --" + key)
		}
		if takesValue && !equals {
			i++
			if i == len(args) {
				return invalid("missing --" + key + " value")
			}
			value = args[i] // even -- or a flag-looking token is a value
		}
		if !takesValue && equals {
			if _, err := strconv.ParseBool(value); err != nil {
				return invalid("invalid task-board boolean: " + a)
			}
		}
		switch key {
		case "board-dir", "remote", "remote-board", "insecure":
			// Preserve tokens and values exactly, including duplicates and booleans.
			start := i
			if takesValue && !equals {
				start--
			}
			p.boardFlags = append(p.boardFlags, args[start:i+1]...)
		case "selection-rationale":
			p.rationalePresent = true
		case "allow-parallel":
			p.allowParallel = true
		}
		if key != "agent" && key != "model" && key != "reasoning-effort" && key != "role" {
			continue
		}
		if value == "" {
			return invalid("empty --" + key + " value")
		}
		if _, duplicate := p.locks[key]; duplicate {
			return invalid("duplicate --" + key)
		}
		p.locks[key] = value
	}
	return p, nil
}
