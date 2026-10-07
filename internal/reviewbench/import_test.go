package reviewbench

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func fixture(t *testing.T) ([]byte, recommend.Catalog) {
	t.Helper()
	raw, err := os.ReadFile("testdata/runs.csv")
	if err != nil {
		t.Fatal(err)
	}
	c, err := recommend.LoadCatalog(recommend.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Rows {
		c.Rows[i].Quality.Review = nil
	}
	return raw, c
}
func TestGoldenImport(t *testing.T) {
	raw, c := fixture(t)
	r, err := Import(raw, c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if os.Getenv("EVC_UPDATE_GOLDENS") == "1" {
		if err = os.WriteFile("testdata/import.golden.json", b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/import.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, want) {
		t.Fatalf("golden differs: %s", b)
	}
	if r.LiveRuns != 9 || r.PooledDF != 2 || math.Abs(r.PooledSD-math.Sqrt(8)) > 1e-12 || len(r.Measurements) != 4 || len(r.Unmapped) != 3 {
		t.Fatal(r)
	}
	for _, m := range r.Measurements {
		if m.Candidate.Model == "gpt-6-astra" && math.Abs(*m.Quality.Stderr-math.Sqrt(8)*100/105) > 1e-12 {
			t.Fatal("singleton did not borrow pooled SD")
		}
	}
	applied := Apply(c, r)
	if err = applied.Validate(); err != nil {
		t.Fatal(err)
	}
	for i, row := range applied.Rows {
		before := c.Rows[i]
		row.Quality.Review = nil
		if !reflect.DeepEqual(before, row) {
			t.Fatal("changed non-review catalog fields")
		}
	}
	if !reflect.DeepEqual(Apply(applied, r), applied) {
		t.Fatal("apply is not idempotent")
	}
	empty := Apply(applied, Report{})
	if !reflect.DeepEqual(empty, c) {
		t.Fatal("stale source measurements remain")
	}
	// CSV order changes only the receipt hash, not measurements or pooled noise.
	records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 1, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	var reordered bytes.Buffer
	writer := csv.NewWriter(&reordered)
	writer.WriteAll(records)
	again, err := Import(reordered.Bytes(), c)
	if err != nil {
		t.Fatal(err)
	}
	again.CSVSHA256 = r.CSVSHA256
	if !reflect.DeepEqual(again, r) {
		t.Fatal("row order changes aggregation")
	}
}
func TestMappingIsExact(t *testing.T) {
	for _, tc := range []struct {
		g                     Group
		model, effort, reason string
	}{
		{Group{"Antigravity CLI", "Gemini 3.8 Flash", "high"}, "gemini-3.8-flash-high", "none", ""},
		{Group{"Antigravity CLI", "Gemini 3.8 Flash", "medium"}, "", "", "registry_model_absent"},
		{Group{"Codex CLI", "GPT-6 Astra", "ultra"}, "", "", "effort_unrecognised"},
		{Group{"Codex CLI", "Opus 5.5", "high"}, "", "", "harness_model_mismatch"},
		{Group{"Claude Code / OpenRouter", "Qwen3.8-27B 8-bit", "default"}, "", "", "harness_unmapped"},
		{Group{"Claude Code", "Fable 5", "high"}, "", "", "model_unmapped"},
		{Group{"Muse Code / Meta API", "Muse Spark 1.3", "low"}, "", "", "harness_binding_unverified"},
		{Group{"Muse Code / Meta API", "Muse Spark 1.3", "medium"}, "", "", "harness_binding_unverified"},
		{Group{"Muse Code / Meta API", "Muse Spark 1.3", "high"}, "", "", "harness_binding_unverified"},
		{Group{"Muse Code / Meta API", "Muse Spark 1.3", "xhigh"}, "", "", "harness_binding_unverified"},
		{Group{"Muse Code / Meta API", "Muse Spark 1.3", "max"}, "", "", "harness_binding_unverified"},
	} {
		c, reason := mapGroup(tc.g)
		if reason != tc.reason || c.Model != tc.model || c.Effort != tc.effort {
			t.Fatalf("%+v: %+v %s", tc.g, c, reason)
		}
	}
}
func TestMetaAPIHarnessIsUnmapped(t *testing.T) {
	raw, c := fixture(t)
	// A refresh must remove the previously misattributed measurement.
	for i := range c.Rows {
		if c.Rows[i].Runtime == "muse" {
			n, se := 5, 1.0
			c.Rows[i].Quality.Review = &recommend.QualityValue{Value: 30, Kind: "measured", Source: Source, AsOf: "2026-09-17", Stderr: &se, N: &n}
		}
	}
	r, err := Import(raw, c)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, group := range r.Unmapped {
		if group.Harness == "Muse Code / Meta API" {
			found = true
			if group.Reason != "harness_binding_unverified" || group.Model != "Muse Spark 1.3" || group.Effort != "max" || !reflect.DeepEqual(group.Runs, []string{"muse"}) {
				t.Fatal(group)
			}
		}
	}
	if !found {
		t.Fatal("Meta API run missing from unmapped receipt")
	}
	for _, m := range r.Measurements {
		if m.Candidate.Runtime == "muse" {
			t.Fatal("unverified binding acquired a measurement", m)
		}
	}
	for _, row := range Apply(c, r).Rows {
		if row.Runtime == "muse" && row.Quality.Review != nil {
			t.Fatal("stale Muse review measurement remains", row)
		}
	}
}
func TestRejectInvalidInput(t *testing.T) {
	raw, c := fixture(t)
	for _, bad := range []string{
		strings.Replace(string(raw), ",25,", ",NaN,", 1),
		strings.Replace(string(raw), ",25,", ",106,", 1),
		strings.Replace(string(raw), ",20,4,list", ",-20,4,list", 1),
		strings.Replace(string(raw), ",20,4,list", ",20,Inf,list", 1),
		strings.Replace(string(raw), "2026-09-03", "today", 1),
		strings.Replace(string(raw), "sol-a,", "sol-b,", 1),
		strings.Replace(string(raw), "wall_min", "no_wall", 1),
	} {
		if _, err := Import([]byte(bad), c); err == nil {
			t.Fatalf("accepted invalid CSV: %s", bad)
		}
	}
	single := "run,row_status,harness,model,effort,date,fixed_of_105,wall_min,cost_usd,cost_kind\na,live,Codex CLI,GPT-6 Astra,medium,2026-09-01,35,20,3,list\n"
	if _, err := Import([]byte(single), c); err == nil || !strings.Contains(err.Error(), "pooled variance") {
		t.Fatal(err)
	}
	// Non-list cost is explicit and never reinterpreted as a list price.
	bill := strings.Replace(string(raw), "30,6,list", "30,6,bill", 1)
	r, err := Import([]byte(bill), c)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range r.Unmapped {
		found = found || x.Reason == "cost_not_list_equivalent"
	}
	if !found {
		t.Fatal(r)
	}
}
