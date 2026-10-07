package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/relux-works/curator-model-router/pkg/canonical"
)

type Mode string

const (
	ModeOff       Mode = "off"
	ModeShadow    Mode = "shadow"
	ModeRecommend Mode = "recommend"
	ModeSelect    Mode = "select"
)

// StrategyName names the base strategy only. Headroom.Enabled turns on the
// headroom-aware wrapper; headroom-aware is not a strategy enum value.
type StrategyName string

const (
	StrategyConfigOrder          StrategyName = "config-order"
	StrategyQualityFirst         StrategyName = "quality-first"
	StrategyCostWithQualityFloor StrategyName = "cost-with-quality-floor"
)

type FreeField string

const (
	FreeRuntime        FreeField = "runtime"
	FreeModel          FreeField = "model"
	FreeEffort         FreeField = "effort"
	FreeNetworkProfile FreeField = "network_profile"
	FreeContextProfile FreeField = "context_profile"
)

type Equivalence string

const (
	SamePairAnyHome Equivalence = "same-pair-any-home"
	DeclaredGroups  Equivalence = "declared-groups"
	FitnessBand     Equivalence = "fitness-band"
)

type OnAllReserved string

const (
	ReservedRank    OnAllReserved = "rank"
	ReservedAbstain OnAllReserved = "abstain"
)

type EquivalentPair struct {
	Runtime *string `json:"runtime,omitempty"` // omitted expands to every runtime
	Model   string  `json:"model"`
	Effort  string  `json:"effort"`
}

// WindowFilter marshals as "all" or an ordered list of window ids, per §5.4.
// TODO(decision): an explicit empty list is permitted and matches no windows.
type WindowFilter struct {
	All bool
	IDs []string
}

func (v WindowFilter) MarshalJSON() ([]byte, error) {
	for _, id := range v.IDs {
		if !utf8.ValidString(id) {
			return nil, &canonical.Error{Code: canonical.InvalidJSON, Message: "invalid UTF-8 in window id"}
		}
	}
	if v.All {
		if len(v.IDs) != 0 {
			return nil, fail("routing_invalid_policy", "all windows cannot also name ids")
		}
		return []byte(`"all"`), nil
	}
	if v.IDs == nil {
		return nil, fail("routing_invalid_policy", "window list must be non-nil")
	}
	return json.Marshal(v.IDs)
}
func (v *WindowFilter) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte(`"all"`)) {
		*v = WindowFilter{All: true}
		return nil
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil || ids == nil {
		return fail("routing_unknown_enum", "windows must be all or a list of ids")
	}
	*v = WindowFilter{IDs: ids}
	return nil
}

type HeadroomPolicy struct {
	Enabled        bool               `json:"enabled"`
	Equivalence    Equivalence        `json:"equivalence"`
	Groups         [][]EquivalentPair `json:"groups"`
	ProtectedRoles []string           `json:"protected_roles"`
	ReserveBP      int64              `json:"reserve_bp"`
	BandWidthBP    int64              `json:"band_width_bp"`
	ExpiringBand   int64              `json:"expiring_band"`
	ConserveBand   int64              `json:"conserve_band"`
	Windows        WindowFilter       `json:"windows"`
	OnAllReserved  OnAllReserved      `json:"on_all_reserved"`
}
type RoutingPolicy struct {
	SchemaVersion string         `json:"schema_version"`
	Strategy      StrategyName   `json:"strategy"`
	Mode          Mode           `json:"mode"`
	FreeFieldMask []FreeField    `json:"free_field_mask"`
	Headroom      HeadroomPolicy `json:"headroom"`
	Quality       *QualityPolicy `json:"quality,omitempty"`
	Cost          *CostPolicy    `json:"cost,omitempty"`
}

// DefaultPolicy returns independent, fully materialized defaults. TODO(decision):
// R7 has no policy default mode/strategy: off/config-order and an empty mask are
// the conservative baseline. SchemaVersion is supplied by the caller.
func DefaultPolicy(schemaVersion string) RoutingPolicy {
	return RoutingPolicy{
		SchemaVersion: schemaVersion, Strategy: StrategyConfigOrder, Mode: ModeOff,
		FreeFieldMask: []FreeField{}, Headroom: HeadroomPolicy{
			Enabled: false, Equivalence: SamePairAnyHome, Groups: [][]EquivalentPair{},
			ProtectedRoles: []string{"orchestrator", "reviewer"}, ReserveBP: 2000,
			BandWidthBP: 1000, ExpiringBand: 3, ConserveBand: -2,
			Windows: WindowFilter{All: true}, OnAllReserved: ReservedRank,
		}}
}

// LoadPolicy is a pure byte loader. Defaults are present before unmarshaling,
// preserving explicit false/zero/empty values. Keys must exactly match JSON
// tags. Operator-authored sets are sorted and de-duplicated before validation;
// programmatic Validate still requires strictly ordered sets.
func LoadPolicy(raw []byte) (RoutingPolicy, error) { return loadPolicy(raw, true) }

func loadPolicy(raw []byte, qualityContracts bool) (RoutingPolicy, error) {
	if _, err := canonical.Canonicalize(raw); err != nil {
		return RoutingPolicy{}, err
	}
	if err := checkPolicyKeys(raw); err != nil {
		return RoutingPolicy{}, err
	}
	p := DefaultPolicy("")
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		if _, ok := err.(*Error); ok {
			return RoutingPolicy{}, err
		}
		return RoutingPolicy{}, fail("routing_invalid_policy", err.Error())
	}
	p.FreeFieldMask = normalizeSet(p.FreeFieldMask)
	p.Headroom.ProtectedRoles = normalizeSet(p.Headroom.ProtectedRoles)
	p.Headroom.Windows.IDs = normalizeSet(p.Headroom.Windows.IDs)
	validate := p.validateCore
	if qualityContracts {
		validate = p.Validate
	}
	if err := validate(); err != nil {
		return RoutingPolicy{}, err
	}
	return p, nil
}

// Validate validates a materialized policy. Programmatic callers construct it
// with DefaultPolicy; zero numerics cannot mean "default" because zero is a value.
func (p RoutingPolicy) Validate() error {
	if err := p.validateCore(); err != nil {
		return err
	}
	if p.Quality != nil || p.Cost != nil {
		return p.validateQualityCost()
	}
	return nil
}

// validateCore preserves the pre-B policy checks for historical selection replay.
func (p RoutingPolicy) validateCore() error {
	if p.Headroom.Equivalence != DeclaredGroups && len(p.Headroom.Groups) != 0 {
		return fail("headroom_groups_unused", "headroom.groups requires declared-groups equivalence")
	}
	if err := validateFields(p); err != nil {
		return err
	}
	switch p.Strategy {
	case StrategyConfigOrder, StrategyQualityFirst, StrategyCostWithQualityFloor:
	default:
		return fail("routing_unknown_enum", "unknown strategy")
	}
	switch p.Mode {
	case ModeOff, ModeShadow, ModeRecommend, ModeSelect:
	default:
		return fail("routing_unknown_enum", "unknown mode")
	}
	for _, field := range p.FreeFieldMask {
		switch field {
		case FreeRuntime, FreeModel, FreeEffort, FreeNetworkProfile, FreeContextProfile:
		default:
			return fail("routing_unknown_enum", "unknown free-field mask member")
		}
	}
	if err := canonical.CheckOrdered(p.FreeFieldMask, func(v FreeField) string { return string(v) }); err != nil {
		return err
	}
	h := p.Headroom
	switch h.Equivalence {
	case SamePairAnyHome, DeclaredGroups, FitnessBand:
	default:
		return fail("routing_unknown_enum", "unknown equivalence")
	}
	switch h.OnAllReserved {
	case ReservedRank, ReservedAbstain:
	default:
		return fail("routing_unknown_enum", "unknown on_all_reserved")
	}
	if h.BandWidthBP < 1 {
		return fail("headroom_invalid_band_width", "band_width_bp must be at least 1")
	}
	if h.ReserveBP < 0 || h.ReserveBP > 10000 {
		return fail("headroom_invalid_reserve", "reserve_bp must be between 0 and 10000")
	}
	if h.ExpiringBand <= h.ConserveBand {
		return fail("headroom_invalid_bands", "expiring_band must exceed conserve_band")
	}
	if h.Windows.All && len(h.Windows.IDs) != 0 {
		return fail("routing_invalid_policy", "all windows cannot also name ids")
	}
	if !h.Windows.All && h.Windows.IDs == nil {
		return fail(ContractMissingField, "$.headroom.windows is required; use [] for known-empty")
	}
	if !h.Windows.All {
		if err := checkFields(reflect.ValueOf(h.Windows.IDs), "$.headroom.windows", false); err != nil {
			return err
		}
	}
	if err := canonical.CheckOrdered(h.Windows.IDs, func(v string) string { return v }); err != nil {
		return err
	}
	if err := canonical.CheckOrdered(h.ProtectedRoles, func(v string) string { return v }); err != nil {
		return err
	}
	// TODO(decision): omitted runtime is a wildcard over all runtimes. Checking
	// symbolic intersections equals expansion without consulting a registry/caller.
	// Group order and pair order are explicit policy order, not map/insertion order.
	if h.Equivalence == DeclaredGroups {
		for i, group := range h.Groups {
			for j, pair := range group {
				if pair.Model == "" || pair.Effort == "" || (pair.Runtime != nil && *pair.Runtime == "") {
					return fail("routing_invalid_policy", "group pair must name model/effort and a nonempty optional runtime")
				}
				for k := 0; k <= i; k++ {
					for l, other := range h.Groups[k] {
						if k == i && l >= j {
							break
						}
						if pair.Model == other.Model && pair.Effort == other.Effort && (pair.Runtime == nil || other.Runtime == nil || *pair.Runtime == *other.Runtime) {
							if k == i {
								return fail("headroom_group_duplicate_pair", "pairs intersect within one declared group")
							}
							return fail(string(ReasonHeadroomGroupsOverlap), "declared groups overlap after runtime expansion")
						}
					}
				}
			}
		}
	}
	return nil
}
func (p RoutingPolicy) Digest() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(p)
}

// validAddress checks content-addressed public evidence record references.
func validAddress(ref string, allowRetraction bool) bool {
	prefix, hex, ok := strings.Cut(ref, ":")
	if !ok || len(hex) != 64 || (prefix != "obs" && prefix != "note" && (!allowRetraction || prefix != "ret")) {
		return false
	}
	for _, r := range hex {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// normalizeSet is used only for operator-authored policy sets.
func normalizeSet[T ~string](values []T) []T {
	slices.Sort(values)
	return slices.Compact(values)
}

// Check exact tags recursively before encoding/json can match case-insensitively.
func checkPolicyKeys(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fail("routing_invalid_policy", err.Error())
	}
	return checkJSONKeys(value, reflect.TypeFor[RoutingPolicy](), "$")
}
func checkJSONKeys(value any, typ reflect.Type, path string) error {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[WindowFilter]() {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		obj, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		// Sort for deterministic refusals when several keys are unknown.
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			found := false
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if strings.Split(field.Tag.Get("json"), ",")[0] == key {
					found = true
					if err := checkJSONKeys(obj[key], field.Type, path+"."+key); err != nil {
						return err
					}
					break
				}
			}
			if !found {
				return fail(ContractUnknownField, path+"."+key+" is not a declared JSON tag")
			}
		}
		if typ == reflect.TypeFor[QualityPolicy]() {
			for _, key := range []string{"minimum_fitness_bp", "minimum_coverage_bp"} {
				if _, exists := obj[key]; !exists {
					return fail(ContractMissingField, path+"."+key+" is required")
				}
			}
		}
	case reflect.Slice:
		if arr, ok := value.([]any); ok {
			for i, item := range arr {
				if err := checkJSONKeys(item, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
