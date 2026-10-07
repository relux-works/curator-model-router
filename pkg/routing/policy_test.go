package routing

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

func TestPolicyDefaults(t *testing.T) {
	p, err := LoadPolicy([]byte(`{"schema_version":"v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonical.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := readFixture(t, "policy-default.canonical.json")
	if !bytes.Equal(got, want) {
		t.Fatalf("got=%s want=%s", got, want)
	}
	digest, err := p.Digest()
	wantDigest := strings.TrimSpace(string(readFixture(t, "policy-default.digest")))
	if err != nil || digest != wantDigest {
		t.Fatalf("got=%s err=%v want=%s", digest, err, wantDigest)
	}
	for _, tc := range []struct {
		name      string
		got, want any
	}{
		{"schema_version", p.SchemaVersion, "v1"},
		{"strategy", p.Strategy, StrategyName("config-order")},
		{"mode", p.Mode, Mode("off")},
		{"free_field_mask", p.FreeFieldMask, []FreeField{}},
		{"enabled", p.Headroom.Enabled, false},
		{"equivalence", p.Headroom.Equivalence, Equivalence("same-pair-any-home")},
		{"groups", p.Headroom.Groups, [][]EquivalentPair{}},
		{"protected_roles", p.Headroom.ProtectedRoles, []string{"orchestrator", "reviewer"}},
		{"reserve_bp", p.Headroom.ReserveBP, int64(2000)},
		{"band_width_bp", p.Headroom.BandWidthBP, int64(1000)},
		{"expiring_band", p.Headroom.ExpiringBand, int64(3)},
		{"conserve_band", p.Headroom.ConserveBand, int64(-2)},
		{"windows", p.Headroom.Windows, WindowFilter{All: true}},
		{"on_all_reserved", p.Headroom.OnAllReserved, OnAllReserved("rank")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Fatalf("got=%#v want=%#v", tc.got, tc.want)
			}
		})
	}
	p, err = LoadPolicy([]byte(`{"schema_version":"v1","headroom":{"reserve_bp":0,"expiring_band":0,"conserve_band":-1,"protected_roles":[],"windows":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Headroom.ReserveBP != 0 || p.Headroom.ExpiringBand != 0 || len(p.Headroom.ProtectedRoles) != 0 || p.Headroom.Windows.All {
		t.Fatalf("explicit zero/empty lost: %+v", p)
	}
	a := DefaultPolicy("v1")
	a.Headroom.ProtectedRoles[0] = "changed"
	if DefaultPolicy("v1").Headroom.ProtectedRoles[0] != "orchestrator" {
		t.Fatal("defaults must not share mutable slices")
	}
}
func TestPolicyRefusals(t *testing.T) {
	tests := []struct{ name, raw, code string }{
		{"zero band", `{"headroom":{"band_width_bp":0}}`, "headroom_invalid_band_width"},
		{"negative band", `{"headroom":{"band_width_bp":-1}}`, "headroom_invalid_band_width"},
		{"low reserve", `{"headroom":{"reserve_bp":-1}}`, "headroom_invalid_reserve"},
		{"high reserve", `{"headroom":{"reserve_bp":10001}}`, "headroom_invalid_reserve"},
		{"equal bands", `{"headroom":{"expiring_band":-2}}`, "headroom_invalid_bands"},
		{"reversed bands", `{"headroom":{"expiring_band":-3}}`, "headroom_invalid_bands"},
		{"mode", `{"mode":"future"}`, "routing_unknown_enum"},
		{"strategy", `{"strategy":"future"}`, "routing_unknown_enum"},
		{"equivalence", `{"headroom":{"equivalence":"future"}}`, "routing_unknown_enum"},
		{"reserved", `{"headroom":{"on_all_reserved":"future"}}`, "routing_unknown_enum"},
		{"windows", `{"headroom":{"windows":"session"}}`, "routing_unknown_enum"},
		{"mask", `{"free_field_mask":["future"]}`, "routing_unknown_enum"},
		{"null", `{"headroom":null}`, canonical.Null},
		{"unknown field", `{"typo":1}`, "contract_unknown_field"},
		{"wildcard overlap", `{"headroom":{"equivalence":"declared-groups","groups":[[{"model":"model-a","effort":"high"}],[{"runtime":"runtime-a","model":"model-a","effort":"high"}]]}}`, "headroom_groups_overlap"},
		{"same runtime overlap", `{"headroom":{"equivalence":"declared-groups","groups":[[{"runtime":"runtime-a","model":"model-a","effort":"high"}],[{"runtime":"runtime-a","model":"model-a","effort":"high"}]]}}`, "headroom_groups_overlap"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := append([]byte(`{"schema_version":"v1",`), []byte(tc.raw[1:])...)
			_, err := LoadPolicy(raw)
			assertCode(t, err, tc.code)
		})
	}
	for _, raw := range []string{
		`{"schema_version":"v1","headroom":{"band_width_bp":1,"reserve_bp":0,"expiring_band":-1,"conserve_band":-2}}`,
		`{"schema_version":"v1","headroom":{"reserve_bp":10000}}`,
		`{"schema_version":"v1","headroom":{"equivalence":"declared-groups","groups":[[{"runtime":"runtime-a","model":"model-a","effort":"high"}],[{"runtime":"runtime-b","model":"model-a","effort":"high"}]]}}`,
	} {
		if _, err := LoadPolicy([]byte(raw)); err != nil {
			t.Fatalf("raw=%s err=%v", raw, err)
		}
	}
}

func TestPolicyBaseStrategy(t *testing.T) {
	for _, strategy := range []string{"config-order", "quality-first", "cost-with-quality-floor", "headroom-aware"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", strategy, enabled), func(t *testing.T) {
				raw := fmt.Sprintf(`{"schema_version":"v1","strategy":%q,"headroom":{"enabled":%t}}`, strategy, enabled)
				_, err := LoadPolicy([]byte(raw))
				code := ""
				if strategy == "headroom-aware" {
					code = "routing_unknown_enum"
				}
				assertCode(t, err, code)
			})
		}
	}
}
func TestPolicyExactKeys(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":"v1","Mode":"select","mode":"off"}`,
		`{"schema_version":"v1","mode":"off","Mode":"select"}`,
		`{"schema_version":"v1","HEADROOM":{}}`,
		`{"schema_version":"v1","headroom":{"Reserve_BP":0}}`,
		`{"schema_version":"v1","headroom":{"typo":0}}`,
		`{"schema_version":"v1","headroom":{"groups":[[{"MODEL":"a","effort":"high"}]]}}`,
		`{"schema_version":"v1","headroom":{"groups":[[{"model":"a","effort":"high","extra":1}]]}}`,
	} {
		t.Run(raw, func(t *testing.T) { _, err := LoadPolicy([]byte(raw)); assertCode(t, err, ContractUnknownField) })
	}
}
func TestPolicySetNormalization(t *testing.T) {
	raw := []byte(`{"schema_version":"v1","free_field_mask":["runtime","effort","runtime"],"headroom":{"protected_roles":["reviewer","orchestrator","reviewer"],"windows":["weekly","session","weekly"]}}`)
	p, err := LoadPolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.FreeFieldMask, []FreeField{FreeEffort, FreeRuntime}) || !reflect.DeepEqual(p.Headroom.ProtectedRoles, []string{"orchestrator", "reviewer"}) || !reflect.DeepEqual(p.Headroom.Windows.IDs, []string{"session", "weekly"}) {
		t.Fatalf("unexpected normalization: %+v", p)
	}
	clean, err := LoadPolicy([]byte(`{"schema_version":"v1","free_field_mask":["effort","runtime"],"headroom":{"protected_roles":["orchestrator","reviewer"],"windows":["session","weekly"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	want, err := clean.Digest()
	if err != nil || got != want {
		t.Fatalf("got=%s err=%v want=%s", got, err, want)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*RoutingPolicy)
	}{
		{"mask unordered", func(p *RoutingPolicy) { p.FreeFieldMask = []FreeField{FreeRuntime, FreeEffort} }},
		{"roles unordered", func(p *RoutingPolicy) { p.Headroom.ProtectedRoles = []string{"reviewer", "orchestrator"} }},
		{"windows unordered", func(p *RoutingPolicy) { p.Headroom.Windows = WindowFilter{IDs: []string{"weekly", "session"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultPolicy("v1")
			tc.mutate(&p)
			assertCode(t, p.Validate(), canonical.Unordered)
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*RoutingPolicy)
	}{
		{"mask duplicate", func(p *RoutingPolicy) { p.FreeFieldMask = []FreeField{FreeRuntime, FreeRuntime} }},
		{"roles duplicate", func(p *RoutingPolicy) { p.Headroom.ProtectedRoles = []string{"reviewer", "reviewer"} }},
		{"windows duplicate", func(p *RoutingPolicy) { p.Headroom.Windows = WindowFilter{IDs: []string{"weekly", "weekly"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultPolicy("v1")
			tc.mutate(&p)
			assertCode(t, p.Validate(), canonical.DuplicateKey)
		})
	}
}
func TestPolicyGroupRefusals(t *testing.T) {
	for _, tc := range []struct{ name, equivalence, groups, code string }{
		{"unused same pair", "same-pair-any-home", `[[{"model":"a","effort":"high"}]]`, "headroom_groups_unused"},
		{"unused fitness", "fitness-band", `[[{"model":"a","effort":"high"}]]`, "headroom_groups_unused"},
		{"in group duplicate", "declared-groups", `[[{"runtime":"r","model":"a","effort":"high"},{"runtime":"r","model":"a","effort":"high"}]]`, "headroom_group_duplicate_pair"},
		{"in group wildcard", "declared-groups", `[[{"model":"a","effort":"high"},{"runtime":"r","model":"a","effort":"high"}]]`, "headroom_group_duplicate_pair"},
		{"between groups", "declared-groups", `[[{"model":"a","effort":"high"}],[{"runtime":"r","model":"a","effort":"high"}]]`, "headroom_groups_overlap"},
		{"disjoint runtimes", "declared-groups", `[[{"runtime":"r1","model":"a","effort":"high"}],[{"runtime":"r2","model":"a","effort":"high"}]]`, ""},
		{"disjoint efforts", "declared-groups", `[[{"model":"a","effort":"high"}],[{"model":"a","effort":"low"}]]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadPolicy([]byte(fmt.Sprintf(`{"schema_version":"v1","headroom":{"equivalence":%q,"groups":%s}}`, tc.equivalence, tc.groups)))
			assertCode(t, err, tc.code)
		})
	}
	p := DefaultPolicy("v1")
	p.Headroom.Equivalence = DeclaredGroups
	p.Headroom.Groups = [][]EquivalentPair{nil}
	assertCode(t, p.Validate(), ContractMissingField)
}

func TestPolicyUnknownMarkerAndUnusedGroupPrecedence(t *testing.T) {
	_, err := LoadPolicy([]byte(`{"schema_version":"v1","headroom":{"windows":[""]}}`))
	assertCode(t, err, ContractEmptyField)
	_, err = LoadPolicy([]byte(`{"schema_version":"v1","headroom":{"groups":[[{"model":"","effort":""}]]}}`))
	assertCode(t, err, "headroom_groups_unused")
}
