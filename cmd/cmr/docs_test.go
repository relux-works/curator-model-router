package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDocumentedSpawnExamplesUseSupportedFlags(t *testing.T) {
	inline := regexp.MustCompile("`((?:cmr|task-board) spawn [^`]+)`")
	for _, path := range []string{"../../README.md", "../../docs/integration.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		examples := []string{}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "cmr spawn ") {
				examples = append(examples, line)
			}
		}
		for _, match := range inline.FindAllStringSubmatch(string(data), -1) {
			examples = append(examples, match[1])
		}
		if len(examples) == 0 {
			t.Fatal(path, "no spawn examples checked")
		}
		for _, example := range examples {
			args := strings.Fields(example)
			forwarded := args[2:]
			if args[0] == "cmr" {
				boundary := -1
				for i, a := range args {
					if a == "--" {
						boundary = i
						break
					}
				}
				if boundary < 0 {
					t.Fatal(path, "missing wrapper separator", example)
				}
				forwarded = args[boundary+1:]
			}
			if _, err = parseSpawnArgs(forwarded); err != nil {
				t.Fatal(path, example, err)
			}
			if !strings.Contains(example, "--background") {
				t.Fatal(path, "launch example missing --background", example)
			}
		}
	}
}
