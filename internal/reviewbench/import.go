// Package reviewbench imports the pinned public Bug Hunt CSV without network or
// harness access. Measurements describe the publisher's defect-finding protocol.
package reviewbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/relux-works/curator-model-router/pkg/recommend"
	"github.com/relux-works/skill-agents-management/pkg/vendorplugin/benchdata"
)

const Source = "https://github.com/phuryn/bug-hunt-bench@1217192a6d04e89da3f6106ca3a304d2734882eb"
const RegistryVersion = "skill-agents-management@v0.5.40"
const PinnedCSVSHA256 = "995ee322bab1019790fb58cc6c25af968e688f2952d8e28380e975f222407a74"

type Group struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

func (g Group) key() string { return g.Harness + "\x00" + g.Model + "\x00" + g.Effort }

type Measurement struct {
	Group
	Candidate       recommend.Candidate    `json:"candidate"`
	Quality         recommend.QualityValue `json:"quality"`
	MeanWallMinutes float64                `json:"mean_wall_minutes"`
	MeanListCost    float64                `json:"mean_list_equivalent_cost_usd"`
	Runs            []string               `json:"runs"`
}
type Unmapped struct {
	Group
	Runs   []string `json:"runs"`
	Reason string   `json:"reason"`
}
type Report struct {
	Source       string        `json:"source"`
	Registry     string        `json:"registry"`
	CSVSHA256    string        `json:"csv_sha256"`
	LiveRuns     int           `json:"live_runs"`
	PooledSD     float64       `json:"pooled_sd_fixed_points"`
	PooledDF     int           `json:"pooled_df"`
	Measurements []Measurement `json:"measurements"`
	Unmapped     []Unmapped    `json:"unmapped"`
}

// Table renders the matching configurations on the normalized quality scale.
func (r Report) Table() string {
	var b strings.Builder
	b.WriteString("| Configuration | Mean /100 | n | ± SE /100 | Wall minutes | List-equiv. cost USD |\n|---|---:|---:|---:|---:|---:|\n")
	for _, m := range r.Measurements {
		fmt.Fprintf(&b, "| `%s/%s/%s` | %.3f | %d | %.3f | %.2f | $%.2f |\n", m.Candidate.Runtime, m.Candidate.Model, m.Candidate.Effort, m.Quality.Value, *m.Quality.N, *m.Quality.Stderr, m.MeanWallMinutes, m.MeanListCost)
	}
	return b.String()
}

type run struct {
	id, date, costKind string
	score, wall, cost  float64
	costKnown          bool
}
type groupRuns struct {
	group Group
	runs  []run
}

// Import keeps live rows, groups by exact harness/model/effort and validates
// explicit mappings against compiled registry vocabulary and catalog membership.
// SE is the group's sample SD/sqrt(n); singleton groups borrow the pooled within-
// configuration SD across ALL live groups (sum squared deviations / sum(n-1)).
// A singleton without any pooled degrees of freedom is an error, not zero noise.
func Import(raw []byte, catalog recommend.Catalog) (Report, error) {
	out := Report{Source: Source, Registry: RegistryVersion, CSVSHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Measurements: []Measurement{}, Unmapped: []Unmapped{}}
	if err := catalog.Validate(); err != nil {
		return out, err
	}
	reader := csv.NewReader(bytes.NewReader(raw))
	header, err := reader.Read()
	if err != nil {
		return out, err
	}
	columns := map[string]int{}
	for i, h := range header {
		if _, ok := columns[h]; ok {
			return out, fmt.Errorf("duplicate CSV header %q", h)
		}
		columns[h] = i
	}
	for _, h := range []string{"run", "row_status", "harness", "model", "effort", "date", "fixed_of_105", "wall_min", "cost_usd", "cost_kind"} {
		if _, ok := columns[h]; !ok {
			return out, fmt.Errorf("missing CSV header %q", h)
		}
	}
	groups := map[string]*groupRuns{}
	seen := map[string]bool{}
	for line := 2; ; line++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, fmt.Errorf("CSV line %d: %w", line, err)
		}
		field := func(k string) string { return record[columns[k]] }
		if field("row_status") != "live" {
			continue
		}
		g := Group{field("harness"), field("model"), field("effort")}
		if g.Harness == "" || g.Model == "" || g.Effort == "" || field("run") == "" {
			return out, fmt.Errorf("CSV line %d: missing identity", line)
		}
		if seen[field("run")] {
			return out, fmt.Errorf("CSV line %d: duplicate live run", line)
		}
		seen[field("run")] = true
		r := run{id: field("run"), date: field("date"), costKind: field("cost_kind")}
		if _, err = time.Parse("2006-01-02", r.date); err != nil {
			return out, fmt.Errorf("CSV line %d: invalid date", line)
		}
		for _, v := range []struct {
			name  string
			dst   *float64
			upper float64
		}{{"fixed_of_105", &r.score, 105}, {"wall_min", &r.wall, math.MaxFloat64}, {"cost_usd", &r.cost, math.MaxFloat64}} {
			// Some unmatched public groups publish only aggregate cost. Missing
			// per-run cost is unknown, while their scores still support pooled SD.
			if v.name == "cost_usd" && field(v.name) == "" {
				continue
			}
			x, err := strconv.ParseFloat(field(v.name), 64)
			if err != nil || math.IsNaN(x) || math.IsInf(x, 0) || x < 0 || x > v.upper {
				return out, fmt.Errorf("CSV line %d: invalid %s", line, v.name)
			}
			*v.dst = x
			if v.name == "cost_usd" {
				r.costKnown = true
			}
		}
		if groups[g.key()] == nil {
			groups[g.key()] = &groupRuns{group: g}
		}
		groups[g.key()].runs = append(groups[g.key()].runs, r)
		out.LiveRuns++
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	sse := 0.0
	for _, k := range keys {
		rs := groups[k].runs
		slices.SortFunc(rs, func(a, b run) int { return strings.Compare(a.id, b.id) })
		mean := meanScore(rs)
		for _, r := range rs {
			sse += (r.score - mean) * (r.score - mean)
		}
		out.PooledDF += len(rs) - 1
	}
	if out.PooledDF > 0 {
		out.PooledSD = math.Sqrt(sse / float64(out.PooledDF))
	}
	admitted := map[string]bool{}
	for _, r := range catalog.Rows {
		admitted[r.Key()] = true
	}
	for _, k := range keys {
		gr := groups[k]
		rs := gr.runs
		ids := []string{}
		for _, r := range rs {
			ids = append(ids, r.id)
		}
		c, reason := mapGroup(gr.group)
		if reason == "" && !admitted[c.Key()] {
			reason = "catalog_configuration_absent"
		}
		for _, r := range rs {
			if reason == "" && r.costKind != "list" {
				reason = "cost_not_list_equivalent"
			}
			if reason == "" && !r.costKnown {
				reason = "cost_unavailable"
			}
		}
		if reason != "" {
			out.Unmapped = append(out.Unmapped, Unmapped{gr.group, ids, reason})
			continue
		}
		n := len(rs)
		mean := meanScore(rs)
		sd := out.PooledSD
		if n > 1 {
			sum := 0.0
			for _, r := range rs {
				sum += (r.score - mean) * (r.score - mean)
			}
			sd = math.Sqrt(sum / float64(n-1))
		} else if out.PooledDF == 0 {
			return out, fmt.Errorf("singleton %s/%s/%s has no pooled variance support", gr.group.Harness, gr.group.Model, gr.group.Effort)
		}
		se := sd / math.Sqrt(float64(n)) * 100 / 105
		wall, cost := 0.0, 0.0
		asOf := ""
		for _, r := range rs {
			wall += r.wall
			cost += r.cost
			if r.date > asOf {
				asOf = r.date
			}
		}
		out.Measurements = append(out.Measurements, Measurement{Group: gr.group, Candidate: c, Quality: recommend.QualityValue{Value: mean * 100 / 105, Kind: "measured", Source: Source, AsOf: asOf, Stderr: &se, N: &n}, MeanWallMinutes: wall / float64(n), MeanListCost: cost / float64(n), Runs: ids})
	}
	slices.SortFunc(out.Measurements, func(a, b Measurement) int { return strings.Compare(a.Candidate.Key(), b.Candidate.Key()) })
	return out, nil
}
func meanScore(rs []run) float64 {
	sum := 0.0
	for _, r := range rs {
		sum += r.score
	}
	return sum / float64(len(rs))
}

// Public display names are explicit aliases, never fuzzy guesses. In particular,
// the hosted Qwen harnesses never map to a local engine, and the Meta API
// harness never maps to the Muse CLI runtime: that execution binding is
// unverified, so its runs are reported as unmapped rather than measured.
// Effort-bearing Gemini ids consume the external effort; the registry effort
// remains exactly "none".
func mapGroup(g Group) (recommend.Candidate, string) {
	if g.Harness == "Muse Code / Meta API" {
		return recommend.Candidate{}, "harness_binding_unverified"
	}
	runtime, ok := map[string]string{"Codex CLI": "codex", "Claude Code": "claude", "Antigravity CLI": "agy"}[g.Harness]
	if !ok {
		return recommend.Candidate{}, "harness_unmapped"
	}
	model, ok := map[string]string{
		"GPT-6 Astra": "gpt-6-astra", "GPT-6.1 Sol": "gpt-6.1-sol", "GPT-6 Sol": "gpt-6-sol", "GPT-6 Luna": "gpt-6-luna",
		"Fable 5.1": "claude-fable-5-1", "Opus 5.5": "claude-opus-5-5", "Sonnet 5.5": "claude-sonnet-5-5", "Haiku 4.5": "claude-haiku-4-5",
	}[g.Model]
	effort := g.Effort
	if runtime == "agy" {
		base, found := map[string]string{"Gemini 3.5 Flash": "gemini-3.5-flash", "Gemini 3.6 Flash": "gemini-3.6-flash", "Gemini 3.7 Flash": "gemini-3.7-flash", "Gemini 3.8 Flash": "gemini-3.8-flash"}[g.Model]
		if found && slices.Contains([]string{"low", "medium", "high"}, effort) {
			model = base + "-" + effort
			effort = "none"
			ok = true
		}
	}
	if !ok {
		return recommend.Candidate{}, "model_unmapped"
	}
	// Prevent known display names being transferred to the wrong harness.
	expected := map[string]string{"codex": "gpt-", "claude": "claude-", "agy": "gemini-"}[runtime]
	if !strings.HasPrefix(model, expected) {
		return recommend.Candidate{}, "harness_model_mismatch"
	}
	for _, fact := range benchdata.RegistryFacts() {
		if fact.ModelID == model {
			if !slices.Contains(fact.Efforts, effort) {
				return recommend.Candidate{}, "effort_unrecognised"
			}
			return recommend.Candidate{Runtime: runtime, Model: model, Effort: effort}, ""
		}
	}
	return recommend.Candidate{}, "registry_model_absent"
}

// Apply replaces review measurements from this source, clearing stale rows while
// preserving other sources and every non-review catalog field.
func Apply(c recommend.Catalog, report Report) recommend.Catalog {
	c.Rows = slices.Clone(c.Rows)
	values := map[string]recommend.QualityValue{}
	for _, m := range report.Measurements {
		values[m.Candidate.Key()] = m.Quality
	}
	for i := range c.Rows {
		r := &c.Rows[i]
		if r.Quality.Review != nil && r.Quality.Review.Source == Source {
			r.Quality.Review = nil
		}
		if q, ok := values[r.Key()]; ok {
			r.Quality.Review = &q
		}
	}
	return c
}
