package main

import (
	"bytes"
	"errors"
	"fmt"
	"runtime/debug"
	"testing"
)

func stubBuildInfo(t *testing.T, info *debug.BuildInfo, ok bool) {
	t.Helper()
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
	t.Cleanup(func() { readBuildInfo = old })
}

func TestRun(t *testing.T) {
	stubBuildInfo(t, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true)
	tests := []struct {
		name     string
		args     []string
		code     int
		out, err string
	}{
		{"version", []string{"version"}, 0, "cmr v1.2.3\n", ""},
		{"version json", []string{"version", "--json"}, 0, "{\"version\":\"v1.2.3\"}\n", ""},
		{"help", []string{"help"}, 0, "Usage: cmr <command> [--json]\n\nCommands:\n  help              Show help\n  config show       Inspect config layers and state/data roots [--json]\n  migrate           Copy legacy files to swarma/router [--dry-run] [--json]\n  version           Show version\n  route             Route a frozen input bundle\n  explain           Explain a recorded decision\n  replay            Replay a recorded decision\n  headroom explain  Explain a usage snapshot\n  evidence          Import and inspect evidence (import, import-evalrun FILE --mapping FILE, ls, show, unresolved, rebind)\n  note              Add or retract an internal note\n  suitability       Derive the role-suitability view\n  local             Import public base scores, validate coefficients, or decide offline\n  recommend         Select a task-aware launch configuration\n  usage             Refresh or show cached provider usage\n  spawn             Recommend and invoke task-board spawn\n  shadow report     Summarize shadow spawn observations\n", ""},
		{"help json", []string{"help", "--json"}, 0, "{\"commands\":[\"help\",\"version\",\"route\",\"explain\",\"replay\",\"headroom explain\",\"evidence\",\"note\",\"suitability\",\"recommend\",\"local\",\"usage\",\"spawn\",\"shadow report\",\"config show\",\"migrate\"]}\n", ""},
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

// The reported version comes from the toolchain's build info: a released module
// version wins, a checkout build reports devel with its revision, and the old
// hard-coded placeholder never appears.
func TestBuildVersion(t *testing.T) {
	rev := "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"released", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.3"}}, true, "v0.3.3"},
		{"pseudo", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.4-0.20261018000000-0123456789ab"}}, true, "v0.3.4-0.20261018000000-0123456789ab"},
		{"devel with revision", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: rev}}}, true, "devel+0123456789ab"},
		{"devel dirty", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: "true"}}}, true, "devel+0123456789ab-dirty"},
		{"devel clean flag", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: "false"}}}, true, "devel+0123456789ab"},
		{"devel no vcs", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, "devel"},
		{"empty version", &debug.BuildInfo{}, true, "devel"},
		{"no build info", nil, false, "devel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubBuildInfo(t, tc.info, tc.ok)
			if got := buildVersion(); got != tc.want {
				t.Fatalf("buildVersion() = %q, want %q", got, tc.want)
			}
			var out bytes.Buffer
			if code := run([]string{"version"}, &out, &bytes.Buffer{}); code != 0 || out.String() != "cmr "+tc.want+"\n" {
				t.Fatalf("cmr version = (%d, %q)", code, out.String())
			}
		})
	}
}
