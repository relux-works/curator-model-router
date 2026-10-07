package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestCLIUsageRefreshAndRecommendationRefresh(t *testing.T) {
	root := cliEnvironment(t)
	payload, err := os.ReadFile("../../internal/quota/testdata/codex.json")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err = json.Compact(&compact, payload); err != nil {
		t.Fatal(err)
	}
	payload = compact.Bytes()
	fixture := filepath.Join(root, "quota.json")
	if err = os.WriteFile(fixture, payload, 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "reads")
	t.Setenv("FAKE_LOG", log)
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$FAKE_LOG"
IFS= read -r init || exit 91
printf '{"id":1,"result":{"codexHome":"%s/.codex"}}\n' "$HOME"
IFS= read -r initialized || exit 92
IFS= read -r request || exit 93
printf '{"id":2,"result":'
/bin/cat '` + fixture + `'
printf '}\n'
IFS= read -r eof && exit 94
exit 0
`
	if err = os.WriteFile(filepath.Join(root, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"usage", "refresh", "--runtime", "codex", "--json"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), `"state":"exact"`) {
		t.Fatal(code, out.String(), stderr.String())
	}
	firstLog, _ := os.ReadFile(log)
	if strings.Contains(out.String(), root) {
		t.Fatal("home exposed in usage projection")
	}
	out.Reset()
	if code := run([]string{"usage", "show", "--json"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), `"state":"exact"`) {
		t.Fatal(code, out.String(), stderr.String())
	}
	file := candidateFile(t, root, false)
	out.Reset()
	if code := run([]string{"recommend", "--role", "developer", "--task-class", "code.implement", "--candidates", file, "--agent", "codex", "--refresh", "--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	lastLog, _ := os.ReadFile(log)
	if !bytes.Equal(firstLog, lastLog) {
		t.Fatal("refresh bypassed throttle")
	}
	paths, _ := filepath.Glob(filepath.Join(cmrio.StateRoot(), "decisions", "*.json"))
	if len(paths) != 1 {
		t.Fatal(paths)
	}
	b, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var record recommend.DecisionRecord
	if err = json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	if len(record.Inputs.Usage.Facts) != 1 || record.Inputs.Usage.Facts[0].Runtime != "codex" {
		t.Fatal(record.Inputs.Usage)
	}
}
