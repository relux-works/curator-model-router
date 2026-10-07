package cmrio

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/recommend"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

func TestTOMLPolicy(t *testing.T) {
	raw, err := os.ReadFile("../../docs/standing-rules.toml")
	if err != nil {
		t.Fatal(err)
	}
	p, err := DecodePolicy(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules) == 0 || p.Rules[0].Source == "" || p.TierEdges.S != 62 {
		t.Fatalf("policy: %+v", p)
	}
	p, err = DecodePolicy([]byte("allow_metered = false\nfanout_k = 2\n[difficulties.standard]\nminimum_tier = 'A'\n[headroom]\nwindows = ['weekly']\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Difficulties["standard"].Objective != "standard" || p.Headroom.Windows.IDs[0] != "weekly" {
		t.Fatalf("overlay: %+v", p)
	}
	for _, raw := range []string{"unknown = true", "fanout_k = 0", "allow_metered = true\nallow_metered = false", "[tier_edges]\ns = 1"} {
		if _, err := DecodePolicy([]byte(raw), false); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestInvalidPolicyRetainsOnlyRawMode(t *testing.T) {
	for _, tc := range []struct {
		raw, mode string
		json      bool
	}{
		{"mode = 'shadow'\nfanout_k = [", "shadow", false},
		{"mode = 'shadow'\nstandard_a_max_cost_ratio = nan", "shadow", false},
		{"mode = 'shadow'\nstandard_a_max_cost_ratio = inf", "shadow", false},
		{"mode = 'select'\nfanout_k = [", "select", false},
		{"mode = 'recommend'\nfanout_k = [", "recommend", false},
		{"\"mode\" = \"sha\\u0064ow\"\nfanout_k = [", "shadow", false},
		{"mode = '''\nshadow'''\nfanout_k = [", "shadow", false},
		{"[rules.when]\nmode = 'shadow'\nfanout_k = [", "", false},
		{"message = '''\nmode = 'shadow'\n'''\nfanout_k = [", "", false},
		{"mode.nested = 'shadow'\nfanout_k = [", "", false},
		{`{"mode":"shadow","fanout_k":`, "shadow", true},
		{`{"mode":"shadow","unknown":true}`, "shadow", true},
		{`{"rules":{"mode":"shadow"},"fanout_k":`, "", true},
		{`{"mode":false,"fanout_k":`, "", true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			p, err := DecodePolicy([]byte(tc.raw), tc.json)
			if err == nil {
				t.Fatal("invalid policy accepted")
			}
			want := recommend.Policy{}
			want.Mode = routing.Mode(tc.mode)
			if !reflect.DeepEqual(p, want) {
				t.Fatalf("invalid routing policy leaked or mode lost: got %+v, want %+v", p, want)
			}
		})
	}
}
func TestConfigPrecedence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	catalog, err := LoadCatalog("")
	if err != nil || len(catalog.Rows) == 0 {
		t.Fatalf("embedded %v", err)
	}
	p, err := LoadPolicy("")
	if err != nil || p.Mode != "select" {
		t.Fatalf("defaults %v", err)
	}
	if _, err = LoadPolicy(filepath.Join(root, "absent.toml")); err == nil {
		t.Fatal("explicit missing policy")
	}
	if _, err = LoadCatalog(filepath.Join(root, "absent.json")); err == nil {
		t.Fatal("explicit missing catalog")
	}
	path := filepath.Join(root, "config", "curator", "model-router", "catalog.json")
	if err = WriteAtomic(path, []byte(`{"schema_version":"catalog-v1","rows":[]}`)); err != nil {
		t.Fatal(err)
	}
	catalog, err = LoadCatalog("")
	if err != nil || len(catalog.Rows) != 0 {
		t.Fatal(catalog, err)
	}
	explicit := filepath.Join(root, "catalog.json")
	if err = WriteAtomic(explicit, recommend.DefaultCatalog()); err != nil {
		t.Fatal(err)
	}
	catalog, err = LoadCatalog(explicit)
	if err != nil || len(catalog.Rows) == 0 {
		t.Fatal(catalog, err)
	}
}
