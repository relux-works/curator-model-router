package evidence

import "github.com/relux-works/curator-model-router/pkg/routing"

// TODO(decision): evidence-v1 identifies the wire schema; kind discriminates
// evidence-import and evidence-snapshot without overloading schema_version.
const SchemaVersion = "evidence-v1"

type Identity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type Reference struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func (r Reference) Address() string { return r.ID + "@" + r.Version }

type CategoryCoverage struct {
	Category string `json:"category"`
	Note     string `json:"note"`
}
type Scale struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}
type Metric struct {
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	Direction   string `json:"direction"`
	Scale       *Scale `json:"scale,omitempty"`
	Description string `json:"description"`
}
type Benchmark struct {
	ID             string             `json:"id"`
	Version        string             `json:"version"`
	Publisher      string             `json:"publisher"`
	Dataset        string             `json:"dataset,omitempty"`
	Split          string             `json:"split,omitempty"`
	ProtocolDigest string             `json:"protocol_digest,omitempty"`
	Categories     []CategoryCoverage `json:"categories"`
	Metrics        []Metric           `json:"metrics"`
	GradingMethod  string             `json:"grading_method"`
	Citation       string             `json:"citation,omitempty"`
}

func (b Benchmark) Address() string { return b.ID + "@" + b.Version }

type Fingerprint struct {
	ModelID          string                 `json:"model_id"`
	Runtime          string                 `json:"runtime,omitempty"`
	HarnessVersion   string                 `json:"harness_version,omitempty"`
	Effort           string                 `json:"effort,omitempty"`
	EngineProfile    *routing.EngineProfile `json:"engine_profile,omitempty"`
	ToolProfile      string                 `json:"tool_profile,omitempty"`
	ExecutionProfile string                 `json:"execution_profile,omitempty"`
}
type Facets struct {
	Languages   []string `json:"languages,omitempty"`
	Platforms   []string `json:"platforms,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	ContextSize string   `json:"context_size,omitempty"`
	Horizon     string   `json:"horizon,omitempty"`
}
type Uncertainty struct {
	Kind  string  `json:"kind"`
	Value float64 `json:"value"`
}
type TransferFrom struct {
	ModelID string `json:"model_id,omitempty"`
	Effort  string `json:"effort,omitempty"`
}
type Transfer struct {
	From        TransferFrom `json:"from"`
	RuleID      string       `json:"rule_id"`
	RuleVersion string       `json:"rule_version"`
}
type Cost struct {
	TokensIn  *int64   `json:"tokens_in,omitempty"`
	TokensOut *int64   `json:"tokens_out,omitempty"`
	USD       *float64 `json:"usd,omitempty"`
	WallS     *float64 `json:"wall_s,omitempty"`
}
type Provenance struct {
	Source         string `json:"source"`
	RetrievedAt    string `json:"retrieved_at"`
	RawArtifactRef string `json:"raw_artifact_ref"`
	OriginRef      string `json:"origin_ref"`
}
type Observation struct {
	ID                string       `json:"id,omitempty"`
	BenchmarkRef      Reference    `json:"benchmark_ref"`
	Subject           Fingerprint  `json:"subject"`
	SubjectResolution string       `json:"subject_resolution"`
	Categories        []string     `json:"categories"`
	Facets            *Facets      `json:"facets,omitempty"`
	Metric            string       `json:"metric"`
	Value             float64      `json:"value"`
	SampleCount       *int64       `json:"sample_count,omitempty"`
	Uncertainty       *Uncertainty `json:"uncertainty,omitempty"`
	EvidenceKind      string       `json:"evidence_kind"`
	Transfer          *Transfer    `json:"transfer,omitempty"`
	Supersedes        []string     `json:"supersedes,omitempty"`
	GradingMethod     string       `json:"grading_method"`
	Cost              *Cost        `json:"cost,omitempty"`
	Provenance        Provenance   `json:"provenance"`
	ObservedAt        string       `json:"observed_at"`
}
type NoteSubject struct {
	ModelID             string   `json:"model_id"`
	Runtime             string   `json:"runtime,omitempty"`
	Efforts             []string `json:"efforts,omitempty"`
	HarnessVersionRange string   `json:"harness_version_range,omitempty"`
}
type Claim struct {
	Polarity   string   `json:"polarity"`
	Statement  string   `json:"statement"`
	Categories []string `json:"categories"`
	Facets     *Facets  `json:"facets,omitempty"`
}
type EvidenceRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}
type Author struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Note struct {
	ID           string        `json:"id,omitempty"`
	Subject      NoteSubject   `json:"subject"`
	Claim        Claim         `json:"claim"`
	Basis        string        `json:"basis"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs"`
	Confidence   string        `json:"confidence"`
	Author       Author        `json:"author"`
	CreatedAt    string        `json:"created_at"`
	ReviewBy     string        `json:"review_by"`
	Supersedes   []string      `json:"supersedes,omitempty"`
}
type Retraction struct {
	ID     string `json:"id,omitempty"`
	Target string `json:"target"`
	Reason string `json:"reason"`
	Author Author `json:"author"`
	At     string `json:"at"`
}
type Import struct {
	SchemaVersion string        `json:"schema_version"`
	Kind          string        `json:"kind"`
	Source        string        `json:"source"`
	Importer      Identity      `json:"importer"`
	ImportedAt    string        `json:"imported_at"`
	MeasuredBy    string        `json:"measured_by"`
	Benchmarks    []Benchmark   `json:"benchmarks"`
	Observations  []Observation `json:"observations"`
	Notes         []Note        `json:"notes"`
	Retractions   []Retraction  `json:"retractions"`
}
type RegistryModel struct {
	ModelID string   `json:"model_id"`
	Efforts []string `json:"efforts"`
}

// Registry is a frozen data-only projection; no registration or launch methods.
type Registry struct {
	SchemaVersion string          `json:"schema_version"`
	Reference     Identity        `json:"reference"`
	Models        []RegistryModel `json:"models"`
}
type Snapshot struct {
	SchemaVersion   string   `json:"schema_version"`
	Kind            string   `json:"kind"`
	Imports         []string `json:"imports"`
	TaxonomyVersion string   `json:"taxonomy_version"`
	RegistryRef     Identity `json:"registry_ref"`
	RegistryDigest  string   `json:"registry_digest"`
}
type Issue struct {
	Code   string `json:"code"`
	Ref    string `json:"ref"`
	Detail string `json:"detail"`
}
type Report struct {
	Issues []Issue `json:"issues"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string          { return e.Code + ": " + e.Message }
func refuse(code, message string) error { return &Error{code, message} }
