package cmrio

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func configEnvironment(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	old := SystemConfigRoot
	SystemConfigRoot = filepath.Join(root, "system")
	t.Cleanup(func() { SystemConfigRoot = old })
	return root
}
func putConfig(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := WriteAtomic(path, data); err != nil {
		t.Fatal(err)
	}
}

// Each highest present layer wins whole, flags dominate all layers, and an
// entirely absent configuration has exactly the existing built-in behavior.
func TestSharedConfigLayers(t *testing.T) {
	for _, kind := range []string{"policy", "catalog"} {
		for _, winner := range []string{"flag", "user", "system", "legacy", "default"} {
			t.Run(kind+"/"+winner, func(t *testing.T) {
				root := configEnvironment(t)
				explicit := filepath.Join(root, configName(kind))
				paths := []string{explicit, filepath.Join(ConfigRoot(), configName(kind)), filepath.Join(SystemConfigRoot, configName(kind)), LegacyConfigPath(kind)}
				layers := []string{"flag", "user", "system", "legacy", "default"}
				index := 0
				for layers[index] != winner {
					index++
				}
				for i := index; i < 4; i++ {
					raw := []byte(fmt.Sprintf("fanout_k = %d\n", i+1))
					if kind == "catalog" {
						var catalog map[string]json.RawMessage
						if err := json.Unmarshal(recommend.DefaultCatalog(), &catalog); err != nil {
							t.Fatal(err)
						}
						var rows []json.RawMessage
						if err := json.Unmarshal(catalog["rows"], &rows); err != nil {
							t.Fatal(err)
						}
						catalog["rows"], _ = json.Marshal(rows[:i])
						raw, _ = json.Marshal(catalog)
					}
					putConfig(t, paths[i], raw)
				}
				flag := ""
				if winner == "flag" {
					flag = explicit
				}
				var source ConfigSource
				if kind == "policy" {
					got, src, err := LoadPolicyWithSource(flag)
					if err != nil {
						t.Fatal(err)
					}
					source = src
					if winner == "default" && !reflect.DeepEqual(got, recommend.DefaultPolicy()) {
						t.Fatal("default policy changed", got)
					}
					if winner != "default" && got.FanOutK != index+1 {
						t.Fatal("file not loaded", got)
					}
				} else {
					got, src, err := LoadCatalogWithSource(flag)
					if err != nil {
						t.Fatal(err)
					}
					source = src
					if winner == "default" {
						want, _ := recommend.LoadCatalog(recommend.DefaultCatalog())
						if !reflect.DeepEqual(got, want) {
							t.Fatal("default catalog changed")
						}
					} else if len(got.Rows) != index {
						t.Fatal("catalog merged with lower layer", got)
					}
				}
				if source.Layer != winner {
					t.Fatal(source)
				}
				if winner != "default" && source.Path != paths[index] {
					t.Fatal(source)
				}
				for _, candidate := range source.Candidates {
					wantExists := false
					for i := index; i < 4; i++ {
						wantExists = wantExists || candidate.Path == paths[i]
					}
					if candidate.Exists != wantExists {
						t.Fatal(candidate)
					}
				}
			})
		}
	}
}

// Higher malformed, unreadable, and dangling files produce typed refusals
// instead of exposing the valid lower layer; missing explicit files also refuse.
func TestSharedConfigRefusesHigherLayer(t *testing.T) {
	for _, kind := range []string{"policy", "catalog"} {
		for _, layer := range []string{"flag", "user", "system", "legacy"} {
			for _, failure := range []string{"malformed", "unreadable", "dangling"} {
				t.Run(kind+"/"+layer+"/"+failure, func(t *testing.T) {
					root := configEnvironment(t)
					path := filepath.Join(root, configName(kind))
					if layer == "user" {
						path = filepath.Join(ConfigRoot(), configName(kind))
					}
					if layer == "system" {
						path = filepath.Join(SystemConfigRoot, configName(kind))
					}
					if layer == "legacy" {
						path = LegacyConfigPath(kind)
					}
					if layer != "legacy" {
						raw := []byte("fanout_k = 3")
						if kind == "catalog" {
							raw = recommend.DefaultCatalog()
						}
						putConfig(t, LegacyConfigPath(kind), raw)
					}
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					switch failure {
					case "malformed":
						putConfig(t, path, []byte("broken ["))
					case "unreadable":
						// A directory at a file path is unreadable even when tests run as root.
						if err := os.Mkdir(path, 0700); err != nil {
							t.Fatal(err)
						}
					case "dangling":
						if err := os.Symlink(filepath.Join(root, "missing"), path); err != nil {
							t.Fatal(err)
						}
					}
					flag := ""
					if layer == "flag" {
						flag = path
					}
					var err error
					if kind == "policy" {
						_, err = LoadPolicy(flag)
					} else {
						_, err = LoadCatalog(flag)
					}
					var refusal *recommend.Refusal
					code := recommend.InvalidPolicy
					if kind == "catalog" {
						code = recommend.InvalidCatalog
					}
					if !errors.As(err, &refusal) || refusal.Code != code {
						t.Fatal("did not refuse higher layer", err)
					}
				})
			}
		}
	}
	configEnvironment(t)
	for _, kind := range []string{"policy", "catalog"} {
		_, err := ResolveConfig(kind, filepath.Join(t.TempDir(), "missing"))
		if err == nil {
			t.Fatal("missing flag accepted", kind)
		}
	}
}

// XDG roots and HOME fallbacks use the shared element; legacy roots remain
// available only for migration/history. Path inspection creates nothing.
func TestSharedRoots(t *testing.T) {
	root := configEnvironment(t)
	for _, c := range []struct{ got, want string }{
		{ConfigRoot(), filepath.Join(root, "XDG_CONFIG_HOME", "swarma", "router")},
		{StateRoot(), filepath.Join(root, "XDG_STATE_HOME", "swarma", "router")},
		{DataRoot(), filepath.Join(root, "XDG_DATA_HOME", "swarma", "router")},
	} {
		if c.got != c.want {
			t.Fatal(c)
		}
	}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, "")
	}
	if ConfigRoot() != filepath.Join(root, ".config", "swarma", "router") || StateRoot() != filepath.Join(root, ".local", "state", "swarma", "router") || DataRoot() != filepath.Join(root, ".local", "share", "swarma", "router") {
		t.Fatal("HOME fallbacks")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("inspection wrote files", entries, err)
	}
}
