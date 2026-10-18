// Package cmrio owns the command layer's configuration and decision IO.
package cmrio

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/relux-works/curator-model-router/pkg/recommend"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

func LoadCatalog(path string) (recommend.Catalog, error) {
	catalog, _, err := LoadCatalogWithSource(path)
	return catalog, err
}
func LoadCatalogWithSource(path string) (recommend.Catalog, ConfigSource, error) {
	source, err := ResolveConfig("catalog", path)
	if err != nil {
		return recommend.Catalog{}, source, err
	}
	raw := recommend.DefaultCatalog()
	if source.Layer != "default" {
		raw, err = os.ReadFile(source.Path)
		if err != nil {
			return recommend.Catalog{}, source, configReadError("catalog")
		}
	}
	catalog, err := recommend.LoadCatalog(raw)
	return catalog, source, err
}

// PreparePolicy reads one frozen policy snapshot and extracts only its execution
// mode. Full decoding/validation can then happen after a shadow child's Start.
// An implicit missing policy yields defaults; an explicit missing policy refuses.
func PreparePolicy(path string) (routing.Mode, func() (recommend.Policy, error)) {
	mode, _, load := PreparePolicyWithSource(path)
	return mode, load
}
func PreparePolicyWithSource(path string) (routing.Mode, ConfigSource, func() (recommend.Policy, error)) {
	source, err := ResolveConfig("policy", path)
	if err == nil && source.Layer == "default" {
		return recommend.DefaultPolicy().Mode, source, func() (recommend.Policy, error) { return recommend.DefaultPolicy(), nil }
	}
	var b []byte
	if err == nil {
		b, err = os.ReadFile(source.Path)
	}
	if err != nil {
		return "", source, func() (recommend.Policy, error) { return recommend.Policy{}, configReadError("policy") }
	}
	asJSON := strings.EqualFold(filepath.Ext(source.Path), ".json")
	mode := rawPolicyMode(b, asJSON)
	if mode == "" {
		mode = recommend.DefaultPolicy().Mode
	}
	return mode, source, func() (recommend.Policy, error) { return DecodePolicy(b, asJSON) }
}
func LoadPolicy(path string) (recommend.Policy, error) {
	policy, _, err := LoadPolicyWithSource(path)
	return policy, err
}
func LoadPolicyWithSource(path string) (recommend.Policy, ConfigSource, error) {
	_, source, load := PreparePolicyWithSource(path)
	policy, err := load()
	return policy, source, err
}

// rawPolicyMode reads only the top-level declaration, independently of strict
// policy decoding and TOML-to-JSON conversion. It is an execution hint only.
// Stop at the first declaration; later syntax errors cannot erase it. Malformed
// input before the declaration or a non-string value leaves the mode unknown.
func rawPolicyMode(b []byte, asJSON bool) routing.Mode {
	if asJSON {
		d := json.NewDecoder(bytes.NewReader(b))
		token, err := d.Token()
		if err != nil || token != json.Delim('{') {
			return ""
		}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return ""
			}
			var value json.RawMessage
			if err := d.Decode(&value); err != nil {
				return ""
			}
			if key == "mode" {
				var mode string
				if json.Unmarshal(value, &mode) == nil {
					return routing.Mode(mode)
				}
				return ""
			}
		}
		return ""
	}
	var parser unstable.Parser
	parser.Reset(b)
	for parser.NextExpression() {
		x := parser.Expression()
		if x.Kind == unstable.Table || x.Kind == unstable.ArrayTable {
			// Subsequent keys belong to a table, never to the policy root.
			return ""
		}
		if x.Kind != unstable.KeyValue {
			continue
		}
		key := x.Key()
		if !key.Next() || string(key.Node().Data) != "mode" || key.Next() {
			continue
		}
		if value := x.Value(); value.Kind == unstable.String {
			return routing.Mode(value.Data)
		}
		return ""
	}
	return ""
}

func DecodePolicy(b []byte, asJSON bool) (p recommend.Policy, err error) {
	// On every load/parse/conversion/validation failure return only the raw mode
	// hint. Never expose an invalid partial policy to routing callers.
	defer func() {
		if err != nil {
			p = recommend.Policy{Mode: rawPolicyMode(b, asJSON)}
		}
	}()
	raw := b
	if !asJSON {
		var wire map[string]any
		if err := toml.NewDecoder(bytes.NewReader(b)).Decode(&wire); err != nil {
			return recommend.Policy{}, &recommend.Refusal{Code: recommend.InvalidPolicy, Message: "invalid TOML policy"}
		}
		raw, err = json.Marshal(wire)
		if err != nil {
			return recommend.Policy{}, &recommend.Refusal{Code: recommend.InvalidPolicy, Message: "invalid TOML policy values"}
		}
	}
	return recommend.LoadPolicy(raw)
}
func SaveDecision(root string, record recommend.DecisionRecord) error {
	b, err := record.JSON()
	if err != nil {
		return err
	}
	return WriteAtomic(filepath.Join(root, "decisions", strings.TrimPrefix(record.DecisionID, "sha256:")+".json"), append(b, '\n'))
}
func WriteAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cmr-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
