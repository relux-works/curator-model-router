package evidence

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/relux-works/skill-agents-management/pkg/vendorplugin/benchdata"
)

const (
	routerModule     = "github.com/relux-works/curator-model-router"
	benchDataPackage = "github.com/relux-works/skill-agents-management/pkg/vendorplugin/benchdata"
)

func forbiddenEvidenceImport(path string) bool {
	if path == "os/exec" || path == "github.com/relux-works/skill-agents-management/pkg/vendorplugin" {
		return true
	}
	if strings.Contains("/"+path+"/", "/agentic/") {
		return true
	}
	vendor := "github.com/relux-works/skill-agents-management/pkg/vendorplugin/"
	return strings.HasPrefix(path, vendor) && path != benchDataPackage && !strings.HasPrefix(path, benchDataPackage+"/")
}

// Follow imports in every production source file, ignoring platform suffixes
// and build tags. This conservative union includes tagged transitive imports.
// No go subprocess or module-cache lookup is used. Only the dependency guard
// reads source; export/parity use the compiled accessors exclusively.
func inspectEvidenceClosure(start string, load func(string) (map[string]string, error)) (int, error) {
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(path string) error {
		if seen[path] {
			return nil
		}
		seen[path] = true
		if forbiddenEvidenceImport(path) {
			return fmt.Errorf("forbidden evidence dependency: %s", path)
		}
		if path == "C" {
			return nil
		}
		sources, err := load(path)
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			return fmt.Errorf("empty production scope: %s", path)
		}
		names := make([]string, 0, len(sources))
		for name := range sources {
			names = append(names, name)
		}
		sort.Strings(names)
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
	err := visit(start)
	return len(seen), err
}

func evidenceClosureLoader(routerRoot, benchDir string) func(string) (map[string]string, error) {
	return func(path string) (map[string]string, error) {
		var dir string
		owned := true
		switch {
		case strings.HasPrefix(path, routerModule+"/"):
			dir = filepath.Join(routerRoot, strings.TrimPrefix(path, routerModule+"/"))
		case path == benchDataPackage:
			dir = benchDir
		case strings.HasPrefix(path, benchDataPackage+"/"):
			dir = filepath.Join(benchDir, strings.TrimPrefix(path, benchDataPackage+"/"))
		case !strings.Contains(strings.Split(path, "/")[0], "."):
			dir = filepath.Join(runtime.GOROOT(), "src", filepath.FromSlash(path))
			owned = false
		default:
			return nil, fmt.Errorf("undeclared third-party evidence dependency: %s", path)
		}
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
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			// Standard-library ignore files are standalone generators/templates,
			// rather than package sources. Never exclude module-owned tagged files.
			if !owned && strings.Contains(string(raw), "//go:build ignore\n") {
				continue
			}
			sources[name] = string(raw)
		}
		return sources, nil
	}
}

func TestEvidenceImportClosureAllBuildTags(t *testing.T) {
	routerRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Resolve the leaf's source from its compiled function location, rather than
	// inspecting go env, module caches, go list, or executable vendor adapters.
	pc := reflect.ValueOf(benchdata.BugHuntRows).Pointer()
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		t.Fatal("accessor source location unavailable")
	}
	source, _ := fn.FileLine(pc)
	if !filepath.IsAbs(source) {
		t.Fatal("dependency guard needs untrimmed source paths")
	}
	count, err := inspectEvidenceClosure(routerModule+"/pkg/evidence", evidenceClosureLoader(routerRoot, filepath.Dir(source)))
	if err != nil {
		t.Fatal(err)
	}
	if count < 10 {
		t.Fatal("guard did not traverse the complete dependency closure")
	}
	t.Logf("all-build-tags evidence production closure: %d packages; no vendor implementation, agentic, or os/exec", count)
}

func TestEvidenceClosureGuardRejectsTaggedTransitiveImports(t *testing.T) {
	for _, dependency := range []string{
		"os/exec",
		"github.com/relux-works/skill-agents-management/pkg/vendorplugin",
		"github.com/relux-works/skill-agents-management/pkg/vendorplugin/vendors/openai",
		"github.com/relux-works/skill-agents-management/pkg/agentic",
		"github.com/relux-works/skill-agents-management/pkg/agentic/systems/codex",
	} {
		t.Run(dependency, func(t *testing.T) {
			sources := map[string]map[string]string{
				routerModule + "/pkg/evidence": {"entry.go": "package evidence\nimport _ \"" + benchDataPackage + "\"\n"},
				benchDataPackage:               {"helper_windows.go": "//go:build future_tag && windows\n\npackage benchdata\nimport _ \"" + benchDataPackage + "/helper\"\n"},
				benchDataPackage + "/helper":   {"tagged.go": "//go:build ignore\n\npackage helper\nimport _ \"" + dependency + "\"\n"},
			}
			_, err := inspectEvidenceClosure(routerModule+"/pkg/evidence", func(path string) (map[string]string, error) {
				if files, ok := sources[path]; ok {
					return files, nil
				}
				return nil, fmt.Errorf("unexpected scope: %s", path)
			})
			if err == nil || !strings.Contains(err.Error(), "forbidden evidence dependency: "+dependency) {
				t.Fatalf("tagged transitive dependency escaped: %v", err)
			}
		})
	}
}

func TestEvidenceClosureGuardFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		load func(string) (map[string]string, error)
	}{
		{"missing", func(string) (map[string]string, error) { return nil, os.ErrNotExist }},
		{"empty", func(string) (map[string]string, error) { return map[string]string{}, nil }},
		{"invalid", func(string) (map[string]string, error) { return map[string]string{"broken.go": "not Go"}, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := inspectEvidenceClosure(routerModule+"/pkg/evidence", tc.load); err == nil {
				t.Fatal("uninspected scope admitted")
			}
		})
	}
	// The filesystem loader must retain module-owned files under every tag,
	// including ignore, while excluding tests from the production boundary.
	root := t.TempDir()
	dir := filepath.Join(root, "pkg", "evidence")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, source string }{
		{"hidden_windows.go", "//go:build ignore\n\npackage evidence\nimport _ \"os/exec\"\n"},
		{"allowed_test.go", "package evidence\n"},
	} {
		if err := os.WriteFile(filepath.Join(dir, tc.name), []byte(tc.source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := inspectEvidenceClosure(routerModule+"/pkg/evidence", evidenceClosureLoader(root, "unused"))
	if err == nil || !strings.Contains(err.Error(), "forbidden evidence dependency: os/exec") {
		t.Fatalf("loader discarded a tagged production file: %v", err)
	}
}
