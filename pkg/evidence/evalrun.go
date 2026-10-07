package evidence

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

const EvalRunSchema = "evalrun-v1"
const EvalRunMappingSchema = "evalrun-mapping-v1"

type EvalRunSubject struct {
	ModelName        string                 `json:"model_name"`
	RuntimeName      string                 `json:"runtime_name,omitempty"`
	HarnessVersion   string                 `json:"harness_version,omitempty"`
	Effort           string                 `json:"effort,omitempty"`
	EngineProfile    *routing.EngineProfile `json:"engine_profile,omitempty"`
	ToolProfile      string                 `json:"tool_profile,omitempty"`
	ExecutionProfile string                 `json:"execution_profile,omitempty"`
}
type EvalRunResult struct {
	ResultID       string         `json:"result_id"`
	Subject        EvalRunSubject `json:"subject"`
	Categories     []string       `json:"categories"`
	Facets         *Facets        `json:"facets,omitempty"`
	Metric         string         `json:"metric"`
	Value          float64        `json:"value"`
	SampleCount    int64          `json:"sample_count"`
	Uncertainty    *Uncertainty   `json:"uncertainty,omitempty"`
	GradingMethod  string         `json:"grading_method"`
	Cost           *Cost          `json:"cost,omitempty"`
	ObservedAt     string         `json:"observed_at"`
	RawArtifactRef string         `json:"raw_artifact_ref"`
	Supersedes     []string       `json:"supersedes,omitempty"`
}
type EvalRun struct {
	SchemaVersion string          `json:"schema_version"`
	RunID         string          `json:"run_id"`
	ExportedAt    string          `json:"exported_at"`
	MappingRef    Identity        `json:"mapping_ref"`
	MappingDigest string          `json:"mapping_digest"`
	Benchmark     Benchmark       `json:"benchmark"`
	Results       []EvalRunResult `json:"results"`
}
type EvalRunNameMapping struct {
	ExternalName string `json:"external_name"`
	InternalID   string `json:"internal_id"`
}
type EvalRunMapping struct {
	SchemaVersion string               `json:"schema_version"`
	Name          string               `json:"name"`
	Version       string               `json:"version"`
	Models        []EvalRunNameMapping `json:"models"`
	Runtimes      []EvalRunNameMapping `json:"runtimes"`
}

func (m EvalRunMapping) Validate() error {
	if m.SchemaVersion != EvalRunMappingSchema {
		return refuse("evidence_evalrun_schema", "unsupported mapping schema")
	}
	if !known(m.Name) || !known(m.Version) || m.Models == nil || m.Runtimes == nil {
		return refuse("evidence_missing_field", "mapping requires identity and explicit collections")
	}
	for _, rows := range [][]EvalRunNameMapping{m.Models, m.Runtimes} {
		if err := canonical.CheckOrdered(rows, func(x EvalRunNameMapping) string { return x.ExternalName }); err != nil {
			return err
		}
		for _, x := range rows {
			if x.ExternalName == "" || x.InternalID == "" {
				return refuse("evidence_missing_field", "mapping names must be nonempty")
			}
		}
	}
	return nil
}
func (m EvalRunMapping) Digest() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(m)
}

var evalRunID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (v EvalRun) Validate() error {
	if v.SchemaVersion != EvalRunSchema {
		return refuse("evidence_evalrun_schema", "unsupported export schema")
	}
	if !validDigest(v.Benchmark.ProtocolDigest) {
		return refuse("evidence_evalrun_protocol_required", "exact known protocol digest required")
	}
	if !evalRunID.MatchString(v.RunID) || v.Results == nil {
		return refuse("evidence_evalrun_invalid_result", "run id and explicit results required")
	}
	if _, err := instant(v.ExportedAt); err != nil {
		return refuse("evidence_invalid_time", "invalid exported_at")
	}
	if !known(v.Benchmark.ID) || !known(v.Benchmark.Version) {
		return refuse("evidence_evalrun_invalid_result", "exact benchmark identity required")
	}
	if v.Benchmark.ID == "internal-review-set" && (!known(v.Benchmark.Dataset) || !known(v.Benchmark.Split)) {
		return refuse("evidence_evalrun_invalid_result", "review dataset revision and split required")
	}
	seen := map[string]bool{}
	facts := map[string][]byte{}
	for _, x := range v.Results {
		key := x.ResultID + "\x00" + x.Metric
		if seen[key] {
			return refuse("evidence_evalrun_result_conflict", "duplicate result/metric")
		}
		seen[key] = true
		if !evalRunID.MatchString(x.ResultID) || x.Subject.ModelName == "" || x.RawArtifactRef == "" || x.SampleCount <= 0 || x.Categories == nil {
			return refuse("evidence_evalrun_invalid_result", "invalid required result facts")
		}
		if !finite(x.Value) {
			return refuse("evidence_invalid_value", "nonfinite result")
		}
		if _, err := instant(x.ObservedAt); err != nil {
			return refuse("evidence_invalid_time", "invalid observed_at")
		}
		shared, err := canonical.MarshalRecord(struct {
			Subject  EvalRunSubject `json:"subject"`
			Artifact string         `json:"artifact"`
			At       string         `json:"at"`
			Count    int64          `json:"count"`
		}{x.Subject, x.RawArtifactRef, x.ObservedAt, x.SampleCount})
		if err != nil {
			return err
		}
		if old, ok := facts[x.ResultID]; ok && !bytes.Equal(old, shared) {
			return refuse("evidence_evalrun_result_conflict", "shared result facts differ")
		}
		facts[x.ResultID] = shared
		if len(x.Categories) == 0 {
			return refuse("evidence_invalid_category", "completed result requires categories")
		}
		if err := stringSet(x.Categories); err != nil {
			return err
		}
		if err := stringSet(x.Supersedes); err != nil {
			return err
		}
		for _, target := range x.Supersedes {
			if !validAddress(target) || !strings.HasPrefix(target, "obs:") {
				return refuse("evidence_invalid_target", "evalrun supersedes must name observations")
			}
		}
		if err := facetsOK(x.Facets); err != nil {
			return err
		}
	}
	if err := canonical.CheckOrdered(v.Results, func(x EvalRunResult) string { return x.ResultID + "\x00" + x.Metric }); err != nil {
		return err
	}
	// Validate the benchmark before Prepare can sort its source collections.
	doc := EmptyImport("internal-evals", Identity{"evalrun", "validation"}, v.ExportedAt)
	doc.Benchmarks = []Benchmark{v.Benchmark}
	return doc.Validate()
}

// EvalRunImport decodes source bytes strictly; artifact references are never opened.
func EvalRunImport(raw []byte, m EvalRunMapping, r Registry) (Import, Report, error) {
	var v EvalRun
	if err := Decode(raw, &v); err != nil {
		return Import{}, Report{}, err
	}
	return ImportEvalRun(v, m, r)
}
func ImportEvalRun(v EvalRun, m EvalRunMapping, r Registry) (Import, Report, error) {
	if err := v.Validate(); err != nil {
		return Import{}, Report{}, err
	}
	md, err := m.Digest()
	if err != nil {
		return Import{}, Report{}, err
	}
	if v.MappingRef != (Identity{m.Name, m.Version}) || v.MappingDigest != md {
		return Import{}, Report{}, refuse("evidence_evalrun_mapping_mismatch", "export does not bind supplied mapping")
	}
	lookup := func(rows []EvalRunNameMapping, s string) (string, bool) {
		for _, x := range rows {
			if x.ExternalName == s {
				return x.InternalID, true
			}
		}
		return s, false
	}
	doc := EmptyImport("internal-evals", Identity{"evalrun", "1+map." + strings.TrimPrefix(md, "sha256:")}, v.ExportedAt)
	doc.Benchmarks = []Benchmark{v.Benchmark}
	unresolvedRuntime := map[string]string{}
	unrecognisedEffort := map[string]string{}
	for _, x := range v.Results {
		model, mapped := lookup(m.Models, x.Subject.ModelName)
		runtime, runtimeMapped := lookup(m.Runtimes, x.Subject.RuntimeName)
		resolution := "unresolved"
		if mapped && r.Resolves(model) && (x.Subject.RuntimeName == "" || runtimeMapped) {
			resolution = "resolved"
		}
		origin := "evalrun:" + v.RunID + ":" + x.ResultID
		if mapped && r.Resolves(model) && known(x.Subject.Effort) && !r.Accepts(model, x.Subject.Effort) {
			unrecognisedEffort[origin] = x.Subject.Effort
		}
		if x.Subject.RuntimeName != "" && !runtimeMapped {
			unresolvedRuntime[origin] = x.Subject.RuntimeName
		}
		count := x.SampleCount
		doc.Observations = append(doc.Observations, Observation{BenchmarkRef: Reference{v.Benchmark.ID, v.Benchmark.Version}, Subject: Fingerprint{ModelID: model, Runtime: runtime, HarnessVersion: x.Subject.HarnessVersion, Effort: x.Subject.Effort, EngineProfile: x.Subject.EngineProfile, ToolProfile: x.Subject.ToolProfile, ExecutionProfile: x.Subject.ExecutionProfile}, SubjectResolution: resolution, Categories: x.Categories, Facets: x.Facets, Metric: x.Metric, Value: x.Value, SampleCount: &count, Uncertainty: x.Uncertainty, EvidenceKind: "measured", Supersedes: x.Supersedes, GradingMethod: x.GradingMethod, Cost: x.Cost, ObservedAt: x.ObservedAt, Provenance: Provenance{"internal-evals", v.ExportedAt, x.RawArtifactRef, origin}})
	}
	doc, report, err := Prepare(doc, r)
	if err != nil {
		return doc, report, err
	}
	for _, o := range doc.Observations {
		if token, ok := unrecognisedEffort[o.Provenance.OriginRef]; ok {
			report.Issues = append(report.Issues, Issue{"effort_unrecognised", o.ID, token})
		}
		if name, ok := unresolvedRuntime[o.Provenance.OriginRef]; ok {
			report.Issues = append(report.Issues, Issue{"runtime_unresolved", o.ID, name})
		}
	}
	return doc, MergeReports(report), nil
}

// MergeReports preserves importer diagnostics through store publication.
func MergeReports(reports ...Report) Report {
	out := Report{Issues: []Issue{}}
	seen := map[Issue]bool{}
	for _, r := range reports {
		for _, i := range r.Issues {
			if !seen[i] {
				seen[i] = true
				out.Issues = append(out.Issues, i)
			}
		}
	}
	canonical.SortByKey(out.Issues, func(i Issue) string { return i.Code + "\x00" + i.Ref + "\x00" + i.Detail })
	return out
}
func (s *Store) AddEvalRun(raw []byte, m EvalRunMapping) (ImportResult, error) {
	doc, report, err := EvalRunImport(raw, m, s.registry)
	if err != nil {
		return ImportResult{}, err
	}
	result, err := s.Add(doc)
	if err == nil {
		result.Report = MergeReports(report, result.Report)
	}
	return result, err
}
