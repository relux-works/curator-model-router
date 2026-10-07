package main

import (
	"os"
	"strings"
	"testing"
)

func TestSpecScopesNoQualifiedRefusal(t *testing.T) {
	b, err := os.ReadFile("../../spec/recommend.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	start := strings.Index(text, "On `no_qualified_candidate`")
	if start < 0 {
		t.Fatal("missing refusal contract")
	}
	paragraph := strings.SplitN(text[start:], "\n\n", 2)[0]
	for _, required := range []string{"select/recommend", "shadow", "original launch"} {
		if !strings.Contains(paragraph, required) {
			t.Fatalf("refusal contract must distinguish shadow: %s", paragraph)
		}
	}
}
