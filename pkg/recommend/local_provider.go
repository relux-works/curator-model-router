package recommend

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"sync"
)

const BaseRecordsVersion = "base-model-records-v1"
const PublicBaseExportVersion = "public-base-export-v1"

type BaseProviderInfo struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Terms    string `json:"terms"`
	NeedsKey bool   `json:"needs_key"`
}

// BaseScoreProvider receives caller-owned bytes only. Keyed implementations may
// keep authorization privately; neither records nor decisions have a key field.
// Implementations must be deterministic and perform no network I/O in Import.
type BaseScoreProvider interface {
	Info() BaseProviderInfo
	Import(raw []byte) ([]BaseModelRecord, error)
}

// BaseProviderRegistry is explicit and has no global registration side effects.
type BaseProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]BaseScoreProvider
}

func NewBaseProviderRegistry() *BaseProviderRegistry {
	return &BaseProviderRegistry{providers: map[string]BaseScoreProvider{}}
}
func (r *BaseProviderRegistry) Register(p BaseScoreProvider) error {
	if p == nil {
		return refuse(InvalidLocalCapability, "nil base provider")
	}
	i := p.Info()
	if strings.TrimSpace(i.ID) == "" || i.Version == "" || i.Terms == "" {
		return refuse(InvalidLocalCapability, "provider requires id, version and terms")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = map[string]BaseScoreProvider{}
	}
	if _, ok := r.providers[i.ID]; ok {
		return refuse(InvalidLocalCapability, "duplicate provider id")
	}
	r.providers[i.ID] = p
	return nil
}
func (r *BaseProviderRegistry) List() []BaseProviderInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []BaseProviderInfo{}
	for _, p := range r.providers {
		out = append(out, p.Info())
	}
	slices.SortFunc(out, func(a, b BaseProviderInfo) int { return strings.Compare(a.ID, b.ID) })
	return out
}
func (r *BaseProviderRegistry) Import(id string, raw []byte) (BaseRecordsDocument, error) {
	r.mu.RLock()
	p, ok := r.providers[id]
	r.mu.RUnlock()
	if !ok {
		return BaseRecordsDocument{}, refuse(InvalidLocalCapability, "unknown base provider")
	}
	records, err := p.Import(raw)
	if err != nil {
		return BaseRecordsDocument{}, err
	}
	d := BaseRecordsDocument{SchemaVersion: BaseRecordsVersion, Records: records}
	return d, d.Validate()
}

type BaseRecordsDocument struct {
	SchemaVersion string            `json:"schema_version"`
	Records       []BaseModelRecord `json:"records"`
}

func (d BaseRecordsDocument) Validate() error {
	if d.SchemaVersion != BaseRecordsVersion || len(d.Records) == 0 {
		return refuse(InvalidLocalCapability, "base-model-records-v1 requires records")
	}
	return validateBases(d.Records)
}
func LoadBaseRecords(raw []byte) (BaseRecordsDocument, error) {
	var d BaseRecordsDocument
	if err := strict(raw, &d, InvalidLocalCapability); err != nil {
		return d, err
	}
	return d, d.Validate()
}

// PublicJSONProvider imports operator-exported, explicitly calibrated public
// leaderboard data. Original metric and calibration are mandatory per axis;
// the provider never interprets a raw benchmark index as router quality.
type PublicJSONProvider struct{}

func (PublicJSONProvider) Info() BaseProviderInfo {
	return BaseProviderInfo{ID: "public-json", Version: "1", Terms: "Local public-source export; operator must supply source-specific licence and attribution. No redistribution rights are implied.", NeedsKey: false}
}
func (p PublicJSONProvider) Import(raw []byte) ([]BaseModelRecord, error) {
	var export BaseRecordsDocument
	if err := strict(raw, &export, InvalidLocalCapability); err != nil {
		return nil, err
	}
	if export.SchemaVersion != PublicBaseExportVersion || len(export.Records) == 0 {
		return nil, refuse(InvalidLocalCapability, "public-base-export-v1 requires calibrated records")
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	for i := range export.Records {
		for axis, provenance := range export.Records[i].Provenance {
			provenance.ProviderID = p.Info().ID
			provenance.ProviderVersion = p.Info().Version
			provenance.PayloadDigest = digest
			export.Records[i].Provenance[axis] = provenance
		}
	}
	slices.SortFunc(export.Records, func(a, b BaseModelRecord) int { return strings.Compare(a.ID, b.ID) })
	return export.Records, validateBases(export.Records)
}
func DefaultBaseProviders() *BaseProviderRegistry {
	r := NewBaseProviderRegistry()
	_ = r.Register(PublicJSONProvider{})
	return r
}
