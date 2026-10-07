package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		code     int
		out, err string
	}{
		{"version", []string{"version"}, 0, "cmr 0.0.0-w0\n", ""},
		{"version json", []string{"version", "--json"}, 0, "{\"version\":\"0.0.0-w0\"}\n", ""},
		{"help", []string{"help"}, 0, "Usage: cmr <command> [--json]\n\nCommands:\n  help              Show help\n  version           Show version\n  route             Route a frozen input bundle\n  explain           Explain a recorded decision\n  replay            Replay a recorded decision\n  headroom explain  Explain a usage snapshot\n  evidence          Import and inspect evidence (import, import-evalrun FILE --mapping FILE, ls, show, unresolved, rebind)\n  note              Add or retract an internal note\n  suitability       Derive the role-suitability view\n  recommend         Select a task-aware launch configuration\n  usage             Refresh or show cached provider usage\n  spawn             Recommend and invoke task-board spawn\n", ""},
		{"help json", []string{"help", "--json"}, 0, "{\"commands\":[\"help\",\"version\",\"route\",\"explain\",\"replay\",\"headroom explain\",\"evidence\",\"note\",\"suitability\",\"recommend\",\"usage\",\"spawn\"]}\n", ""},
		{"unknown", []string{"bad"}, 2, "", "cmr_unknown_subcommand: unknown subcommand: bad\n"},
		{"unknown json", []string{"bad", "--json"}, 2, "{\"error\":{\"code\":\"cmr_unknown_subcommand\",\"message\":\"unknown subcommand: bad\"}}\n", ""},
		{"invalid", []string{"version", "bad"}, 2, "", "cmr_invalid_arguments: usage: cmr version [--json]\n"},
		{"invalid json", []string{"help", "bad", "--json"}, 2, "{\"error\":{\"code\":\"cmr_invalid_arguments\",\"message\":\"usage: cmr help [--json]\"}}\n", ""},
		{"newline", []string{"bad\ncommand"}, 2, "", "cmr_unknown_subcommand: unknown subcommand: bad command\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, err bytes.Buffer
			code := run(tc.args, &out, &err)
			if code != tc.code || out.String() != tc.out || err.String() != tc.err {
				t.Fatalf("got (%d, %q, %q), want (%d, %q, %q)", code, out.String(), err.String(), tc.code, tc.out, tc.err)
			}
		})
	}
	var typed *Refusal
	if !errors.As(&Refusal{Code: "example", Message: "test"}, &typed) {
		t.Fatal("refusal must support errors.As")
	}
}

type failFirstWriter struct {
	bytes.Buffer
	failed bool
}

func (w *failFirstWriter) Write(p []byte) (int, error) {
	if !w.failed {
		w.failed = true
		return 0, errors.New("fixture failure")
	}
	return w.Buffer.Write(p)
}
func (w *failFirstWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func TestOutputFailure(t *testing.T) {
	for _, command := range []string{"help", "version"} {
		for _, asJSON := range []bool{false, true} {
			t.Run(command+fmt.Sprint(asJSON), func(t *testing.T) {
				var out failFirstWriter
				var stderr bytes.Buffer
				args := []string{command}
				if asJSON {
					args = append(args, "--json")
				}
				code := run(args, &out, &stderr)
				wantOut, wantErr := "", "cmr_output_failed: could not write "+command+"\n"
				if asJSON {
					wantOut = "{\"error\":{\"code\":\"cmr_output_failed\",\"message\":\"could not write " + command + "\"}}\n"
					wantErr = ""
				}
				if code != 2 || out.String() != wantOut || stderr.String() != wantErr {
					t.Fatalf("got=(%d,%q,%q) want=(2,%q,%q)", code, out.String(), stderr.String(), wantOut, wantErr)
				}
			})
		}
	}
}
