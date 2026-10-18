package cmrio

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

// SystemConfigRoot is replaceable by command-layer tests; production uses /etc.
var SystemConfigRoot = "/etc/swarma/router"

func xdgRoot(key, fallback string) string {
	if root := os.Getenv(key); root != "" {
		return root
	}
	return filepath.Join(os.Getenv("HOME"), fallback)
}
func ConfigRoot() string {
	return filepath.Join(xdgRoot("XDG_CONFIG_HOME", ".config"), "swarma", "router")
}
func StateRoot() string {
	return filepath.Join(xdgRoot("XDG_STATE_HOME", ".local/state"), "swarma", "router")
}
func DataRoot() string {
	return filepath.Join(xdgRoot("XDG_DATA_HOME", ".local/share"), "swarma", "router")
}
func LegacyStateRoot() string {
	return filepath.Join(xdgRoot("XDG_STATE_HOME", ".local/state"), "curator", "model-router")
}
func LegacyDataRoot() string {
	return filepath.Join(xdgRoot("XDG_DATA_HOME", ".local/share"), "curator", "model-router")
}
func LegacyConfigPath(kind string) string {
	if kind == "policy" {
		return filepath.Join(os.Getenv("HOME"), ".curator", "routing.toml")
	}
	return filepath.Join(xdgRoot("XDG_CONFIG_HOME", ".config"), "curator", "model-router", "catalog.json")
}
func configName(kind string) string {
	if kind == "policy" {
		return "routing.toml"
	}
	return "catalog.json"
}

type PathCandidate struct {
	Path   string `json:"path"`
	Layer  string `json:"layer"`
	Exists bool   `json:"exists"`
}
type ConfigSource struct {
	Path       string          `json:"path"`
	Layer      string          `json:"layer"`
	Candidates []PathCandidate `json:"candidates"`
}

// ResolveConfig selects one whole file. A broken symlink is present and must
// refuse when read, rather than exposing a lower-priority configuration.
func ResolveConfig(kind, explicit string) (ConfigSource, error) {
	candidates := []PathCandidate{}
	if explicit != "" {
		candidates = append(candidates, PathCandidate{Path: explicit, Layer: "flag"})
	}
	candidates = append(candidates,
		PathCandidate{Path: filepath.Join(ConfigRoot(), configName(kind)), Layer: "user"},
		PathCandidate{Path: filepath.Join(SystemConfigRoot, configName(kind)), Layer: "system"},
		PathCandidate{Path: LegacyConfigPath(kind), Layer: "legacy"})
	source := ConfigSource{Layer: "default", Candidates: candidates}
	for i := range source.Candidates {
		c := &source.Candidates[i]
		_, err := os.Lstat(c.Path)
		c.Exists = err == nil || !os.IsNotExist(err)
		if source.Layer != "default" {
			continue
		}
		if c.Exists || c.Layer == "flag" {
			source.Path, source.Layer = c.Path, c.Layer
			if err != nil {
				return source, configReadError(kind)
			}
		}
	}
	return source, nil
}
func configReadError(kind string) error {
	code := recommend.InvalidCatalog
	if kind == "policy" {
		code = recommend.InvalidPolicy
	}
	return &recommend.Refusal{Code: code, Message: "cannot read " + kind}
}
func (s ConfigSource) WarnLegacy(kind string, stderr io.Writer) {
	if s.Layer == "legacy" && stderr != nil {
		_, _ = fmt.Fprintf(stderr, "cmr: reading legacy %s %s; move it to %s (cmr migrate)\n", kind, s.Path, filepath.Join(ConfigRoot(), configName(kind)))
	}
}
