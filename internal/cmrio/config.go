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

func StateRoot() string {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		root = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(root, "curator", "model-router")
}
func LoadCatalog(path string) (recommend.Catalog, error) {
	if path == "" {
		root := os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(os.Getenv("HOME"), ".config")
		}
		path = filepath.Join(root, "curator", "model-router", "catalog.json")
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return recommend.LoadCatalog(recommend.DefaultCatalog())
		}
		if err != nil {
			return recommend.Catalog{}, &recommend.Refusal{Code: recommend.InvalidCatalog, Message: "cannot read catalog"}
		}
		return recommend.LoadCatalog(b)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return recommend.Catalog{}, &recommend.Refusal{Code: recommend.InvalidCatalog, Message: "cannot read catalog"}
	}
	return recommend.LoadCatalog(b)
}

// PreparePolicy reads one frozen policy snapshot and extracts only its execution
// mode. Full decoding/validation can then happen after a shadow child's Start.
// An implicit missing policy yields defaults; an explicit missing policy refuses.
func PreparePolicy(path string) (routing.Mode, func() (recommend.Policy, error)) {
	explicit := path != ""
	if !explicit {
		path = filepath.Join(os.Getenv("HOME"), ".curator", "routing.toml")
	}
	b, err := os.ReadFile(path)
	if !explicit && os.IsNotExist(err) {
		return recommend.DefaultPolicy().Mode, func() (recommend.Policy, error) { return recommend.DefaultPolicy(), nil }
	}
	if err != nil {
		return "", func() (recommend.Policy, error) {
			return recommend.Policy{}, &recommend.Refusal{Code: recommend.InvalidPolicy, Message: "cannot read policy"}
		}
	}
	asJSON := strings.EqualFold(filepath.Ext(path), ".json")
	mode := rawPolicyMode(b, asJSON)
	if mode == "" {
		mode = recommend.DefaultPolicy().Mode
	}
	return mode, func() (recommend.Policy, error) { return DecodePolicy(b, asJSON) }
}

func LoadPolicy(path string) (recommend.Policy, error) {
	_, load := PreparePolicy(path)
	return load()
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
