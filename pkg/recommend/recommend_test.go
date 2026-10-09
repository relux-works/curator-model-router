package recommend

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/routing"
)

func fixture(t testing.TB) Request {
	t.Helper()
	raw, err := os.ReadFile("testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Catalog: c, Policy: DefaultPolicy(), Task: TaskProfile{Role: "developer", TaskClass: "code.implement", Platform: "windows"}, Usage: UsageSnapshot{AsOf: 1800000000}, AdmissionSource: "candidates-file"}
	r.Policy.TierEdges = TierEdges{75, 60, 45}
	for _, row := range c.Rows {
		r.Candidates = append(r.Candidates, row.Candidate)
	}
	return r
}
func restrict(r *Request, models ...string) {
	r.Candidates = []Candidate{}
	for _, row := range r.Catalog.Rows {
		if slices.Contains(models, row.Model) {
			r.Candidates = append(r.Candidates, row.Candidate)
		}
	}
}
func ptr[T any](v T) *T { return &v }
func run(t testing.TB, r Request) Recommendation {
	t.Helper()
	got, err := Recommend(r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func selected(t testing.TB, r Recommendation, model string) {
	t.Helper()
	if r.Refusal != nil || r.Selected == nil || r.Selected.Model != model {
		t.Fatalf("want selected %s: %s", model, r.RenderHuman())
	}
}
func explanation(t testing.TB, r Recommendation, model string) CandidateExplanation {
	t.Helper()
	for _, x := range r.Explanation {
		if x.Candidate.Model == model {
			return x
		}
	}
	t.Fatalf("missing %s", model)
	return CandidateExplanation{}
}
func expectCode(t testing.TB, err error, code string) {
	t.Helper()
	var e *Refusal
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func quota(r *Request, model string, used, left, runs int64, freshness routing.Freshness) {
	if r.Usage.UsageKeys == nil {
		r.Usage.UsageKeys = map[string]string{}
	}
	for _, c := range r.Candidates {
		if c.Model != model {
			continue
		}
		key := "usage-" + model
		r.Usage.UsageKeys[c.Key()] = key
		observed := r.Usage.AsOf
		if freshness == routing.Stale {
			observed -= 1000
		}
		r.Usage.Facts = append(r.Usage.Facts, routing.UsageFact{Key: key, Runtime: c.Runtime, State: routing.StateExact, RecordDigest: ptr("sha256:" + strings.Repeat("a", 64)), Windows: []routing.UsageWindow{{ID: "session", Scope: "all", Minutes: 300, UsedBP: ptr(used), ObservedAt: ptr(observed), ResetsAt: ptr(r.Usage.AsOf + left), Freshness: freshness}}})
		r.Usage.Inflight = append(r.Usage.Inflight, routing.Inflight{Key: key, Runs: runs})
	}
}

// These vectors retain the historical selector. New local contracts have separate boundary/replay tests.
func TestHistoricalGoldenScenarios(t *testing.T) {
	var scenarios []struct {
		Name         string   `json:"name"`
		Difficulty   string   `json:"difficulty"`
		Delicate     bool     `json:"delicate"`
		Models       []string `json:"models"`
		Exhausted    bool     `json:"exhausted"`
		Fanout       bool     `json:"fanout"`
		Agent        string   `json:"agent"`
		Model        string   `json:"model"`
		EffortLock   string   `json:"effort_lock"`
		Selected     string   `json:"selected"`
		Effort       string   `json:"effort"`
		FanoutModels []string `json:"fanout_models"`
		Refusal      string   `json:"refusal"`
	}
	raw, err := os.ReadFile("testdata/scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &scenarios); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			r := fixture(t)
			r.Task.Difficulty = scenario.Difficulty
			if scenario.Delicate {
				r.Task.Sensitivity = "delicate"
			}
			if scenario.Fanout {
				r.Task.Pipeline = "fanout"
			}
			if scenario.Models != nil {
				restrict(&r, scenario.Models...)
			}
			r.Locks = Locks{Agent: scenario.Agent, Model: scenario.Model, Effort: scenario.EffortLock}
			if scenario.Exhausted {
				quota(&r, "alpha", 10000, 9000, 0, routing.Fresh)
				quota(&r, "delta", 2000, 1800, 0, routing.Fresh)
			}
			record, err := buildDecision(r, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := Replay(record); err != nil {
				t.Fatal(err)
			}
			got := record.Recommendation
			if scenario.Refusal != "" {
				if got.Refusal == nil || got.Refusal.Code != scenario.Refusal || got.Selected != nil || len(got.FanOut) != 0 {
					t.Fatalf("want refusal: %+v", got)
				}
			} else {
				selected(t, got, scenario.Selected)
			}
			if scenario.Effort != "" && got.Selected.Effort != scenario.Effort {
				t.Fatal("incorrect effort")
			}
			if scenario.Fanout {
				models := []string{}
				families := map[string]bool{}
				for _, c := range got.FanOut {
					models = append(models, c.Model)
					x := explanation(t, got, c.Model)
					if families[x.Family] {
						t.Fatal("duplicate family")
					}
					families[x.Family] = true
				}
				if !reflect.DeepEqual(models, scenario.FanoutModels) {
					t.Fatalf("fanout = %v", models)
				}
			}
			encoded, err := got.JSON()
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, '\n')
			path := "testdata/" + scenario.Name + ".golden.json"
			if os.Getenv("RC_RECOMMEND_UPDATE") == "1" {
				if err := os.WriteFile(path, encoded, 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != string(want) {
				t.Fatalf("golden differs: %s", got.RenderHuman())
			}
		})
	}
}

func TestTaskInference(t *testing.T) {
	for _, tc := range []struct{ class, explicit, difficulty string }{
		{"routine", "", "routine"}, {"docs.write", "", "routine"}, {"research", "", "hard"}, {"planning", "", "hard"}, {"orchestration", "", "hard"},
		{"code.fix", "", "standard"}, {"review.code", "", "standard"}, {"research", "trivial", "trivial"},
	} {
		t.Run(tc.class+tc.explicit, func(t *testing.T) {
			p, err := (TaskProfile{Role: "custom-role", TaskClass: tc.class, Difficulty: tc.explicit}).Normalize()
			if err != nil {
				t.Fatal(err)
			}
			if p.Difficulty != tc.difficulty || p.Sensitivity != "normal" || p.Pipeline != "single" {
				t.Fatal(p)
			}
		})
	}
	for _, p := range []TaskProfile{{Role: "r", TaskClass: "typo"}, {Role: "r", TaskClass: "research", Difficulty: "typo"}, {Role: "r", TaskClass: "research", Pipeline: "typo"}, {Role: "r", TaskClass: "research", Sensitivity: "typo"}, {TaskClass: "routine"}} {
		_, err := p.Normalize()
		expectCode(t, err, InvalidTask)
	}
}
func TestTierBoundaries(t *testing.T) {
	for _, tc := range []struct {
		v, se float64
		tier  Tier
	}{{0, 0, TierC}, {44.9, 0, TierC}, {45, 0, TierB}, {56, 0, TierA}, {62, 0, TierS}, {62, 1, TierA}, {56, 1, TierB}, {45, 1, TierC}, {100, 60, TierC}, {63, 1, TierS}} {
		q := &QualityValue{Value: tc.v, Stderr: ptr(tc.se)}
		if got := TierFor(q, DefaultPolicy()); got != tc.tier {
			t.Fatalf("%v +/- %v: %s", tc.v, tc.se, got)
		}
	}
	if TierFor(nil, DefaultPolicy()) != TierU {
		t.Fatal("unknown scored")
	}
	r := fixture(t)
	restrict(&r, "boundary")
	selected(t, run(t, r), "boundary")
	if explanation(t, run(t, r), "boundary").Tier != TierA {
		t.Fatal("stderr crossing not lower tier")
	}
}
func TestStrictLoaders(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"schema_version":"catalog-v1","rows":null}`, `{"schema_version":"catalog-v1","rows":[],"Rows":[]}`, `{"schema_version":"catalog-v1","rows":[],"rows":[]}`, `{"schema_version":"other","rows":[]}`, `{"schema_version":"catalog-v1"}`, `{"schema_version":"catalog-v1","rows":[]} {}`, `{"schema_version":"catalog-v1","rows":[],"x":"\ud800"}`} {
		_, err := LoadCatalog([]byte(raw))
		expectCode(t, err, InvalidCatalog)
	}
	for _, raw := range []string{`null`, `[]`, `{"allow_metered":null}`, `{"Allow_Metered":true}`, `{"allow_metered":true,"allow_metered":false}`, `{"schema_version":"other"}`, `{"tier_edges":{"s":40}}`, `{"headroom":{"reserve_bp":10001}}`, `{"fanout_k":0}`, `{"difficulties":{"unknown":{"minimum_tier":"A","objective":"quality"}}}`, `{"mode":"off"}`, `{"headroom":{"enabled":true,"bogus":1}}`, `{} {}`} {
		_, err := LoadPolicy([]byte(raw))
		expectCode(t, err, InvalidPolicy)
	}
	p, err := LoadPolicy([]byte(`{"allow_metered":true,"headroom":{"reserve_bp":0,"enabled":false},"difficulties":{"hard":{"minimum_tier":"S"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.AllowMetered || p.Headroom.ReserveBP != 0 || p.Headroom.Enabled || p.Difficulties["hard"].Objective != "quality" || p.Difficulties["hard"].MinimumTier != TierS {
		t.Fatal(p)
	}
	p, err = LoadPolicy(nil)
	if err != nil || !reflect.DeepEqual(p, DefaultPolicy()) {
		t.Fatal(p, err)
	}
	c := fixture(t).Catalog
	for _, tc := range []struct {
		name string
		edit func(*Catalog)
	}{
		{"duplicate", func(c *Catalog) { c.Rows = append(c.Rows, c.Rows[0]) }},
		{"negative_quality", func(c *Catalog) { c.Rows[0].Quality.Coding.Value = -1 }},
		{"too_high", func(c *Catalog) { c.Rows[0].Quality.Coding.Value = 101 }},
		{"bad_kind", func(c *Catalog) { c.Rows[0].Quality.Coding.Kind = "operator" }},
		{"bad_date", func(c *Catalog) { c.Rows[0].Cost.AsOf = "not-a-date" }},
		{"negative_cost", func(c *Catalog) { c.Rows[0].Cost.USDPerTask = ptr(-1.0) }},
		{"negative_stderr", func(c *Catalog) { c.Rows[0].Quality.Coding.Stderr = ptr(-1.0) }},
		{"infinite", func(c *Catalog) { c.Rows[0].Quality.Coding.Value = math.Inf(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) { r := fixture(t).Catalog; tc.edit(&r); expectCode(t, r.Validate(), InvalidCatalog) })
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	json.Unmarshal(raw, &obj)
	rows := obj["rows"].([]any)
	row := rows[0].(map[string]any)
	q := row["quality"].(map[string]any)["coding"].(map[string]any)
	delete(q, "value")
	raw, _ = json.Marshal(obj)
	_, err = LoadCatalog(raw)
	expectCode(t, err, InvalidCatalog)
}

func TestSelectionRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(*Request)
		want    string
		refused bool
	}{
		{"standard_prefers_affordable_a", func(r *Request) { restrict(r, "base", "alpha") }, "alpha", false},
		{"standard_keeps_b_when_a_expensive", func(r *Request) {
			restrict(r, "base", "alpha")
			for i := range r.Catalog.Rows {
				if r.Catalog.Rows[i].Model == "alpha" {
					r.Catalog.Rows[i].Cost.USDPerTask = ptr(.31)
				}
			}
		}, "base", false},
		{"delicate_a_fallback", func(r *Request) { restrict(r, "alpha", "delta"); r.Task.Sensitivity = "delicate" }, "alpha", false},
		{"delicate_b_refuses", func(r *Request) { restrict(r, "base"); r.Task.Sensitivity = "delicate" }, "", true},
		{"unknown_does_not_displace_known", func(r *Request) { restrict(r, "unknown", "base") }, "base", false},
		{"unknown_cannot_prove_floor", func(r *Request) { restrict(r, "unknown") }, "", true},
		{"unguarded_local_cannot_prove_floor", func(r *Request) { restrict(r, "mini", "local") }, "", true},
		{"metered_opt_in_last_resort", func(r *Request) { restrict(r, "meter"); r.Policy.AllowMetered = true }, "meter", false},
		{"subscription_precedes_local_and_metered", func(r *Request) { restrict(r, "alpha", "local", "meter"); r.Policy.AllowMetered = true }, "alpha", false},
		{"empty_admission", func(r *Request) { r.Candidates = nil }, "", true},
		{"effort_lock_cannot_override_constraint", func(r *Request) { restrict(r, "muse"); r.Locks.Effort = "low" }, "", true},
		{"platform_filter", func(r *Request) { restrict(r, "blocked") }, "", true},
		{"role_filter", func(r *Request) {
			restrict(r, "alpha")
			for i := range r.Catalog.Rows {
				if r.Catalog.Rows[i].Model == "alpha" {
					r.Catalog.Rows[i].Constraints.NotFor = []string{"developer"}
				}
			}
		}, "", true},
		{"language_filter", func(r *Request) {
			restrict(r, "alpha")
			r.Task.Language = "go"
			for i := range r.Catalog.Rows {
				if r.Catalog.Rows[i].Model == "alpha" {
					r.Catalog.Rows[i].Constraints.NotFor = []string{"go"}
				}
			}
		}, "", true},
		{"locked_unadmitted_cannot_expand", func(r *Request) { restrict(r, "alpha"); r.Locks.Model = "super" }, "", true},
		{"critical_strongest", func(r *Request) { r.Task.Difficulty = "critical" }, "super", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t)
			tc.edit(&r)
			got := run(t, r)
			if tc.refused {
				if got.Refusal == nil || got.Selected != nil {
					t.Fatal(got)
				}
			} else {
				selected(t, got, tc.want)
			}
		})
	}
}

func TestFrontierPerTaskClass(t *testing.T) {
	r := fixture(t)
	restrict(&r, "super", "sigma")
	r.Task.Difficulty = "hard"
	code := run(t, r)
	selected(t, code, "super")
	if explanation(t, code, "sigma").Frontier {
		t.Fatal("dominated code row is on frontier")
	}
	r.Task.TaskClass = "research"
	research := run(t, r)
	selected(t, research, "sigma")
	if !explanation(t, research, "sigma").Frontier {
		t.Fatal("research failed to use overall")
	}
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	got := run(t, r)
	if explanation(t, got, "delta").Frontier {
		t.Fatal("equal-quality more expensive row not dominated")
	}
}

func TestHeadroomContracts(t *testing.T) {
	for _, tc := range []struct {
		name, role                               string
		usedA, leftA, runsA, usedB, leftB, runsB int64
		freshA                                   routing.Freshness
		want                                     string
	}{
		{"expiring", "developer", 2000, 16200, 0, 2000, 1800, 0, routing.Fresh, "delta"},
		{"over_last", "developer", 10000, 9000, 0, 2000, 9000, 0, routing.Fresh, "delta"},
		{"reserve", "developer", 8500, 1000, 0, 2000, 9000, 0, routing.Fresh, "delta"},
		{"reviewer_protected", "reviewer", 8500, 1000, 0, 2000, 17000, 0, routing.Fresh, "alpha"},
		{"orchestrator_protected", "orchestrator", 8500, 1000, 0, 2000, 17000, 0, routing.Fresh, "alpha"},
		{"spread", "developer", 2000, 9000, 5, 2000, 9000, 0, routing.Fresh, "delta"},
		{"stale_neutral", "developer", 15000, 9000, 0, 5000, 9000, 0, routing.Stale, "alpha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t)
			restrict(&r, "alpha", "delta")
			r.Task.Role = tc.role
			quota(&r, "alpha", tc.usedA, tc.leftA, tc.runsA, tc.freshA)
			quota(&r, "delta", tc.usedB, tc.leftB, tc.runsB, routing.Fresh)
			got := run(t, r)
			selected(t, got, tc.want)
			if tc.freshA == routing.Stale {
				h := explanation(t, got, "alpha").Headroom
				if h.Headroom != nil || h.Band != 0 || h.Over || h.Reserve {
					t.Fatal(h)
				}
			}
		})
	}
	r := fixture(t)
	restrict(&r, "alpha", "delta")
	quota(&r, "alpha", 10000, 9000, 0, routing.Fresh)
	quota(&r, "delta", 2000, 9000, 0, routing.Fresh)
	r.Policy.Headroom.Enabled = false
	selected(t, run(t, r), "alpha")
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	quota(&r, "alpha", 9000, 9000, 0, routing.Fresh)
	quota(&r, "delta", 9000, 9000, 0, routing.Fresh)
	r.Policy.Headroom.OnAllReserved = routing.ReservedAbstain
	if run(t, r).Refusal == nil {
		t.Fatal("all-reserved must abstain")
	}
	r.Policy.Headroom.OnAllReserved = routing.ReservedRank
	if !slices.Contains(explanation(t, run(t, r), "alpha").ReasonCodes, "reserve_breached") {
		t.Fatal("missing reserve breach")
	}
	r = fixture(t)
	restrict(&r, "base", "alpha")
	r.Policy.StandardAMaxCostRatio = 1
	quota(&r, "base", 10000, 9000, 0, routing.Fresh)
	quota(&r, "alpha", 0, 1000, 0, routing.Fresh)
	selected(t, run(t, r), "base")
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	for i := range r.Catalog.Rows {
		if r.Catalog.Rows[i].Model == "delta" {
			r.Catalog.Rows[i].Cost.USDPerTask = ptr(.36)
		}
	}
	quota(&r, "alpha", 10000, 9000, 0, routing.Fresh)
	quota(&r, "delta", 0, 1000, 0, routing.Fresh)
	selected(t, run(t, r), "alpha")
	// Partial, expired, model scope and weekly binding come directly from routing.
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	quota(&r, "alpha", 2000, 1000, 0, routing.Fresh)
	quota(&r, "delta", 5000, 9000, 0, routing.Fresh)
	w := r.Usage.Facts[0].Windows[0]
	w.ID = "weekly"
	w.Freshness = routing.Stale
	w.ObservedAt = ptr(r.Usage.AsOf - 1000)
	r.Usage.Facts[0].Windows = append(r.Usage.Facts[0].Windows, w)
	got := run(t, r)
	h := explanation(t, got, "alpha").Headroom
	if h.Headroom != nil || h.Band != 0 || !slices.Contains(h.ReasonCodes, routing.ReasonQuotaPartial) {
		t.Fatal(h)
	}
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	quota(&r, "alpha", 2000, 1800, 0, routing.Fresh)
	quota(&r, "delta", 2000, 9000, 0, routing.Fresh)
	w = r.Usage.Facts[0].Windows[0]
	w.ID = "weekly"
	w.Minutes = 10080
	w.UsedBP = ptr(int64(8000))
	w.ResetsAt = ptr(r.Usage.AsOf + 544320)
	r.Usage.Facts[0].Windows = append(r.Usage.Facts[0].Windows, w)
	selected(t, run(t, r), "delta")
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	quota(&r, "alpha", 20000, 0, 0, routing.Expired)
	quota(&r, "delta", 5000, 9000, 0, routing.Fresh)
	got = run(t, r)
	selected(t, got, "alpha")
	if explanation(t, got, "alpha").Headroom.Over {
		t.Fatal("expired influenced over")
	}
	r = fixture(t)
	restrict(&r, "alpha")
	quota(&r, "alpha", 2000, 1000, 0, routing.Fresh)
	r.Usage.Facts[0].Windows[0].Scope = "specific"
	h = explanation(t, run(t, r), "alpha").Headroom
	if h.Headroom != nil || !slices.Contains(h.ReasonCodes, routing.ReasonQuotaScopeUnmapped) {
		t.Fatal(h)
	}
}

func TestDeterminismReplayAndIsolation(t *testing.T) {
	r := fixture(t)
	quota(&r, "alpha", 2000, 1000, 3, routing.Fresh)
	quota(&r, "delta", 2000, 9000, 0, routing.Fresh)
	before, _ := json.Marshal(r)
	d, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("mutated input")
	}
	if err := Replay(d); err != nil {
		t.Fatal(err)
	}
	raw, err := d.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDecision(raw); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(r.Catalog.Rows)
	slices.Reverse(r.Candidates)
	slices.Reverse(r.Usage.Facts)
	slices.Reverse(r.Usage.Inflight)
	other, err := BuildDecision(r)
	if err != nil {
		t.Fatal(err)
	}
	if other.DecisionID != d.DecisionID {
		t.Fatal("set storage order changed identity")
	}
	for i := 0; i < 10; i++ {
		if got := run(t, r); got.DecisionID != d.DecisionID {
			t.Fatal("nondeterminism")
		}
	}
	r.Catalog.Rows[0].Cost.USDPerTask = ptr(123.0)
	if err := Replay(d); err != nil {
		t.Fatal("record aliases inputs", err)
	}
	tamper := d
	tamper.DecisionID = "sha256:" + strings.Repeat("f", 64)
	expectCode(t, Replay(tamper), DecisionMismatch)
	tamper = d
	tamper.Recommendation.Selected = &Candidate{Runtime: "not-admitted", Model: "substitution", Effort: "max"}
	id, err := tamper.ContentID()
	if err != nil {
		t.Fatal(err)
	}
	tamper.DecisionID = id
	tamper.Recommendation.DecisionID = id
	expectCode(t, Replay(tamper), DecisionMismatch)
	if !strings.Contains(d.Recommendation.RenderHuman(), "decision=") || !strings.Contains(d.Recommendation.RenderHuman(), "not_admitted") && len(d.Inputs.Candidates) < len(d.Inputs.Catalog.Rows) {
		t.Fatal("explanation missing")
	}
}

func TestInputRefusals(t *testing.T) {
	for _, edit := range []func(*Request){
		func(r *Request) { r.Locks.Model = string([]byte{0xff}) },
		func(r *Request) { r.Usage.AsOf = 0 }, func(r *Request) { r.Candidates = append(r.Candidates, r.Candidates[0]) }, func(r *Request) { r.Candidates[0].Effort = "" },
		func(r *Request) { r.Usage.Inflight = []routing.Inflight{{Key: "k", Runs: -1}} }, func(r *Request) {
			quota(r, "alpha", 2000, 1000, 0, routing.Fresh)
			r.Usage.Facts = append(r.Usage.Facts, r.Usage.Facts[0])
		},
	} {
		r := fixture(t)
		edit(&r)
		_, err := Recommend(r)
		expectCode(t, err, InvalidInput)
	}
	r := fixture(t)
	r.Candidates = []Candidate{{Runtime: "new", Model: "missing", Effort: "max"}}
	got := run(t, r)
	if got.Refusal == nil || !slices.Contains(explanation(t, got, "missing").ReasonCodes, "catalog_missing") {
		t.Fatal(got)
	}
}

func TestCostPrecisionAndMissingValues(t *testing.T) {
	// Cost comparisons and equivalence keep all 64-bit token precision.
	a := Cost{TokensPerTask: ptr(int64(9007199254740992))}
	b := Cost{TokensPerTask: ptr(int64(9007199254740993))}
	if costCompare(a, b) >= 0 || interchangeable(a, b, 0) || costWithinRatio(b, a, 1) {
		t.Fatal("lost integer precision")
	}
	if !interchangeable(Cost{TokensPerTask: ptr(int64(4))}, Cost{TokensPerTask: ptr(int64(5))}, 2500) {
		t.Fatal("inclusive 25% boundary")
	}
	if interchangeable(Cost{}, Cost{}, 2500) {
		t.Fatal("unknown costs are not interchangeable")
	}
	if interchangeable(Cost{USDPerTask: ptr(1.0)}, Cost{TokensPerTask: ptr(int64(1))}, 2500) {
		t.Fatal("mixed units compared")
	}
	if interchangeable(Cost{USDPerTask: ptr(0.0)}, Cost{USDPerTask: ptr(1.0)}, 2500) {
		t.Fatal("zero matched positive cost")
	}
	r := fixture(t)
	r.Policy = Policy{}
	restrict(&r, "alpha")
	selected(t, run(t, r), "alpha")
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	for i := range r.Catalog.Rows {
		if r.Catalog.Rows[i].Model == "alpha" {
			r.Catalog.Rows[i].Cost.USDPerTask = nil
			r.Catalog.Rows[i].Cost.TokensPerTask = ptr(int64(20))
		}
		if r.Catalog.Rows[i].Model == "delta" {
			r.Catalog.Rows[i].Cost.USDPerTask = nil
			r.Catalog.Rows[i].Cost.TokensPerTask = ptr(int64(10))
		}
	}
	selected(t, run(t, r), "delta")
	// Headroom cannot replace a quality-maximising objective's strongest row.
	r = fixture(t)
	restrict(&r, "alpha", "delta")
	r.Task.Difficulty = "hard"
	for i := range r.Catalog.Rows {
		if r.Catalog.Rows[i].Model == "delta" {
			r.Catalog.Rows[i].Quality.Coding.Value = 67
		}
	}
	quota(&r, "alpha", 10000, 9000, 0, routing.Fresh)
	quota(&r, "delta", 0, 1000, 0, routing.Fresh)
	selected(t, run(t, r), "alpha")
}

func FuzzAdmissionConstraintsAndLocks(f *testing.F) {
	f.Add(uint64(0), uint8(0), uint8(0), false, false)
	f.Add(uint64(8191), uint8(0), uint8(0), false, false)
	f.Add(uint64(8191), uint8(3), uint8(7), true, true)
	f.Fuzz(func(t *testing.T, mask uint64, lock, level uint8, delicate, fanout bool) {
		r := fixture(t)
		all := slices.Clone(r.Candidates)
		r.Candidates = []Candidate{}
		for i, c := range all {
			if mask&(1<<i) != 0 {
				r.Candidates = append(r.Candidates, c)
			}
		}
		if int(lock)%4 != 0 {
			c := all[int(level)%len(all)]
			switch lock % 4 {
			case 1:
				r.Locks.Agent = c.Runtime
			case 2:
				r.Locks.Model = c.Model
			case 3:
				r.Locks.Effort = c.Effort
			}
		}
		r.Task.Difficulty = []string{"trivial", "routine", "standard", "hard", "critical"}[int(level)%5]
		if delicate {
			r.Task.Sensitivity = "delicate"
		}
		if fanout {
			r.Task.Pipeline = "fanout"
		}
		r.Policy.AllowMetered = mask&1 != 0
		got := run(t, r)
		chosen := slices.Clone(got.FanOut)
		if got.Selected != nil {
			chosen = append(chosen, *got.Selected)
		}
		for _, c := range chosen {
			if !slices.Contains(r.Candidates, c) {
				t.Fatal("selection expanded admission")
			}
			if r.Locks.Agent != "" && c.Runtime != r.Locks.Agent || r.Locks.Model != "" && c.Model != r.Locks.Model || r.Locks.Effort != "" && c.Effort != r.Locks.Effort {
				t.Fatal("lock violated")
			}
			for _, row := range r.Catalog.Rows {
				if row.Candidate != c {
					continue
				}
				if row.Constraints.OnlyEffort != "" && row.Effort != row.Constraints.OnlyEffort || slices.Contains(row.Constraints.NotFor, r.Task.Platform) {
					t.Fatal("constraint violated")
				}
				if row.Billing == routing.BillingMetered && !r.Policy.AllowMetered {
					t.Fatal("metered without opt-in")
				}
			}
		}
		for _, x := range got.Alternatives {
			if !slices.Contains(r.Candidates, x.Candidate) {
				t.Fatal("alternative expanded admission")
			}
		}
	})
}
