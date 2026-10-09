package recommend

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const localModule = "github.com/relux-works/curator-model-router"

// Inspect every module-owned production file, including inactive build tags.
// Network packages and indirect process/native escape routes are forbidden;
// undeclared third-party dependencies fail closed. No Go subprocess is needed.
func inspectLocalImports(start string, load func(string) (map[string]string, error)) error {
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(path string) error {
		if seen[path] {
			return nil
		}
		seen[path] = true
		if path == "net" || strings.HasPrefix(path, "net/") || slices.Contains([]string{"os/exec", "plugin", "syscall", "unsafe", "C"}, path) {
			return fmt.Errorf("forbidden offline dependency: %s", path)
		}
		if !strings.HasPrefix(path, localModule+"/") {
			if strings.Contains(strings.Split(path, "/")[0], ".") {
				return fmt.Errorf("undeclared offline dependency: %s", path)
			}
			return nil // standard-library leaf
		}
		sources, err := load(path)
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			return fmt.Errorf("empty offline production scope: %s", path)
		}
		names := make([]string, 0, len(sources))
		for name := range sources {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			file, err := parser.ParseFile(token.NewFileSet(), name, sources[name], parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imported := range file.Imports {
				dependency, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return err
				}
				if err := visit(dependency); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(start)
}

func TestLocalProductionImportBoundary(t *testing.T) {
	load := func(path string) (map[string]string, error) {
		dir := filepath.Join("..", "..", strings.TrimPrefix(path, localModule+"/"))
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		sources := map[string]string{}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				continue
			}
			// Other CLI commands intentionally support process spawning. The
			// local tooling entry point has its own import boundary.
			if path == localModule+"/cmd/cmr" && name != "local.go" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			sources[name] = string(raw)
		}
		return sources, nil
	}
	for _, start := range []string{localModule + "/pkg/recommend", localModule + "/cmd/cmr"} {
		if err := inspectLocalImports(start, load); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocalImportBoundaryRejectsNetworkBypasses(t *testing.T) {
	for _, dependency := range []string{"net", "net/http", "net/rpc", "os/exec", "plugin", "syscall", "unsafe", "C", "example.invalid/custom-http-client"} {
		t.Run(dependency, func(t *testing.T) {
			start, helper := localModule+"/pkg/recommend", localModule+"/pkg/offline-helper"
			files := map[string]map[string]string{
				start:  {"local.go": "package recommend\nimport _ " + strconv.Quote(helper) + "\n"},
				helper: {"hidden_windows.go": "//go:build future && windows\n\npackage helper\nimport _ " + strconv.Quote(dependency) + "\n"},
			}
			err := inspectLocalImports(start, func(path string) (map[string]string, error) {
				return files[path], nil
			})
			if err == nil || !strings.Contains(err.Error(), dependency) {
				t.Fatal("offline import guard missed a tagged transitive bypass", dependency, err)
			}
		})
	}
}
