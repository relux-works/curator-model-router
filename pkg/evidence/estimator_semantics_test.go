package evidence

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/canonical"
	"github.com/relux-works/curator-model-router/pkg/routing"
)

func TestEstimatorPinnedNotePriors(t *testing.T) {
	for _, version := range []string{"1", "2"} {
		t.Run("version-"+version, func(t *testing.T) {
			base := estimatorFixture(t, func(i *EstimatorInput) {
				estimatorPrior(i)
				if version == "2" {
					i.Derivation = ReviewDerivationV2()
				}
			})
			_, estimates, _ := estimatorOutput(t, base)
			if estimates[0].Fitness == nil || *estimates[0].Fitness != 8000 || *estimates[0].Coverage != 0 || len(estimates[0].ContributingRecordIDs) != 1 {
				t.Fatalf("pinned operator prior: %+v", estimates[0])
			}
			for _, table := range []string{"confidence", "basis"} {
				weights := base.Derivation.ConfidenceWeights
				if table == "basis" {
					weights = base.Derivation.BasisWeights
				}
				for index, prior := range weights {
					for _, change := range []struct {
						name  string
						value float64
					}{{"zero", 0}, {"adjacent-float", math.Nextafter(prior.Value, 0)}} {
						t.Run(table+"/"+prior.Key+"/"+change.name, func(t *testing.T) {
							input := estimatorCopy(base)
							if table == "confidence" {
								input.Derivation.ConfidenceWeights[index].Value = change.value
							} else {
								input.Derivation.BasisWeights[index].Value = change.value
							}
							// These inputs pass W2 validation but reuse a reviewed version
							// with changed priors; the estimator must refuse them.
							if err := input.Derivation.Validate(); err != nil {
								t.Fatal(err)
							}
							e, err := NewFitnessEstimator(input)
							evalError(t, err, "evidence_invalid_derivation")
							if e != nil {
								t.Fatal("refused priors returned an estimator")
							}
						})
					}
				}
			}
			for _, tc := range []struct {
				name   string
				mutate func(*Derivation)
			}{
				{"extra-confidence", func(d *Derivation) {
					d.ConfidenceWeights = append(d.ConfidenceWeights, Weight{"synthetic", 0.5})
				}},
				{"extra-basis", func(d *Derivation) {
					d.BasisWeights = append(d.BasisWeights, Weight{"synthetic", 0.5})
				}},
				{"unsupported-version", func(d *Derivation) { d.Version = "3" }},
				{"unsupported-name", func(d *Derivation) { d.Name = "synthetic" }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					input := estimatorCopy(base)
					tc.mutate(&input.Derivation)
					if err := input.Derivation.Validate(); err != nil {
						t.Fatal(err)
					}
					_, err := NewFitnessEstimator(input)
					evalError(t, err, "evidence_invalid_derivation")
				})
			}
		})
	}
}

func TestEstimatorMatchingAndLifecycle(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*EstimatorInput)
		fitness  *int64
		coverage int64
		refs     int
		issue    string
	}{
		{"unknown-all-optional-one-discount", func(i *EstimatorInput) {
			f := &i.Fingerprints[0].Fingerprint
			f.Runtime = ""
			f.HarnessVersion = ""
			f.EngineProfile = nil
			f.ToolProfile = ""
			f.ExecutionProfile = ""
		}, estBP(7000), 5000, 1, ""},
		{"unrecognised-observation-effort", func(i *EstimatorInput) { i.Imports[0].Observations[0].Subject.Effort = "ultrahigh" }, nil, 0, 0, ""},
		{"unrecognised-candidate-effort", func(i *EstimatorInput) {
			i.Candidates[0].Effort = "ultrahigh"
			i.Fingerprints[0].Fingerprint.Effort = "ultrahigh"
		}, nil, 0, 0, ""},
		{"known-none-effort", func(i *EstimatorInput) {
			i.Candidates[0].Effort = "none"
			i.Fingerprints[0].Fingerprint.Effort = "none"
			i.Imports[0].Observations[0].Subject.Effort = "none"
		}, estBP(9000), 10000, 1, ""},
		{"cross-effort-never-transfers", func(i *EstimatorInput) { estimatorTransfer(i); i.Imports[0].Observations[0].Subject.Effort = "max" }, nil, 0, 0, ""},
		{"unresolved-subject", func(i *EstimatorInput) { i.Imports[0].Observations[0].SubjectResolution = "unresolved" }, nil, 0, 0, ""},
		{"engine-conflict", func(i *EstimatorInput) { i.Imports[0].Observations[0].Subject.EngineProfile.Name = "other" }, nil, 0, 0, ""},
		{"profile-conflict", func(i *EstimatorInput) { i.Imports[0].Observations[0].Subject.ExecutionProfile = "other" }, nil, 0, 0, ""},
		{"unmapped-metric", func(i *EstimatorInput) { i.Derivation.Normalisations = []Normalisation{} }, nil, 0, 0, "observation_unmapped"},
		{"partial-observation-facets", func(i *EstimatorInput) { i.Requirements.Requirements[0].Facets = &Facets{Languages: []string{"go"}} }, estBP(7000), 5000, 1, ""},
		{"partial-note-facets", func(i *EstimatorInput) {
			estimatorPrior(i)
			i.Requirements.Requirements[0].Facets = &Facets{Platforms: []string{"linux"}}
		}, estBP(6500), 0, 1, ""},
		{"note-runtime-conflict", func(i *EstimatorInput) { estimatorPrior(i); i.Imports[0].Notes[0].Subject.Runtime = "other" }, nil, 0, 0, ""},
		{"note-harness-conflict", func(i *EstimatorInput) {
			estimatorPrior(i)
			i.Imports[0].Notes[0].Subject.HarnessVersionRange = "[2,3]"
		}, nil, 0, 0, ""},
		{"caution-negative", func(i *EstimatorInput) { estimatorPrior(i); i.Imports[0].Notes[0].Claim.Polarity = "caution" }, estBP(2000), 0, 1, ""},
		{"measured-partial-preferred-over-interpolated-direct", func(i *EstimatorInput) {
			o := &i.Imports[0].Observations[0]
			other := estimatorCopy(*o)
			o.Subject.HarnessVersion = ""
			other.EvidenceKind = "interpolated"
			other.Provenance.OriginRef = "another"
			i.Imports[0].Observations = append(i.Imports[0].Observations, other)
		}, estBP(8000), 5000, 2, ""},
		{"reprint-direct-preferred", func(i *EstimatorInput) {
			o := estimatorCopy(i.Imports[0].Observations[0])
			o.Subject.HarnessVersion = ""
			i.Imports[0].Observations = append(i.Imports[0].Observations, o)
		}, estBP(9000), 10000, 1, ""},
		{"reprint-lexical-id", func(i *EstimatorInput) {
			o := estimatorCopy(i.Imports[0].Observations[0])
			o.Provenance.Source = "reprint"
			i.Imports[0].Observations = append(i.Imports[0].Observations, o)
		}, estBP(9000), 10000, 1, ""},
		{"unknown-origin-independent", func(i *EstimatorInput) {
			o := &i.Imports[0].Observations[0]
			o.Provenance.OriginRef = canonical.Unknown
			other := estimatorCopy(*o)
			other.Provenance.Source = "reprint"
			i.Imports[0].Observations = append(i.Imports[0].Observations, other)
		}, estBP(9000), 10000, 2, ""},
		{"conflicting-values-retain-issue", func(i *EstimatorInput) {
			o := estimatorCopy(i.Imports[0].Observations[0])
			o.Value = 0.8
			o.Provenance.Source = "conflict"
			i.Imports[0].Observations = append(i.Imports[0].Observations, o)
		}, estBP(9000), 10000, 1, ""},
		{"supersession", func(i *EstimatorInput) {
			old, _, err := Prepare(i.Imports[0], i.Registry)
			if err != nil {
				panic(err)
			}
			i.Imports[0] = old
			o := estimatorCopy(old.Observations[0])
			o.ID = ""
			o.Value = 0.4
			o.Supersedes = []string{old.Observations[0].ID}
			i.Imports[0].Observations = append(i.Imports[0].Observations, o)
		}, estBP(7000), 10000, 1, ""},
		{"retraction", func(i *EstimatorInput) {
			old, _, err := Prepare(i.Imports[0], i.Registry)
			if err != nil {
				panic(err)
			}
			i.Imports[0] = old
			i.Imports[0].Retractions = []Retraction{{Target: old.Observations[0].ID, Reason: "Synthetic retraction", Author: Author{"human", "synthetic-author"}, At: "2026-10-01T12:00:00Z"}}
		}, nil, 0, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := estimatorFixture(t, tc.mutate)
			e, estimates, _ := estimatorOutput(t, input)
			got := estimates[0]
			if !reflect.DeepEqual(got.Fitness, tc.fitness) || *got.Coverage != tc.coverage || len(got.ContributingRecordIDs) != tc.refs {
				t.Fatalf("estimate=%+v want F=%v C=%d refs=%d", got, tc.fitness, tc.coverage, tc.refs)
			}
			if tc.issue != "" {
				found := false
				for _, issue := range e.View().Issues {
					if issue.Code == tc.issue {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %s in %+v", tc.issue, e.View().Issues)
				}
			}
			if tc.name == "reprint-lexical-id" && got.ContributingRecordIDs[0] != input.Imports[0].Observations[0].ID {
				t.Fatal("did not select lexical id")
			}
		})
	}
	t.Run("real-value-conflict", func(t *testing.T) {
		input := estimatorFixture(t, func(i *EstimatorInput) {
			old, _, err := Prepare(i.Imports[0], i.Registry)
			if err != nil {
				t.Fatal(err)
			}
			i.Imports[0] = old
			for _, value := range []float64{0.2, 0.4} {
				o := estimatorCopy(old.Observations[0])
				o.ID = ""
				o.Value = value
				o.Supersedes = []string{old.Observations[0].ID}
				i.Imports[0].Observations = append(i.Imports[0].Observations, o)
			}
		})
		e, estimates, _ := estimatorOutput(t, input)
		if len(estimates[0].ContributingRecordIDs) != 1 {
			t.Fatal(estimates)
		}
		found := false
		for _, issue := range e.View().Issues {
			if issue.Code == "evidence_conflict" {
				found = true
			}
		}
		if !found {
			t.Fatal(e.View().Issues)
		}
	})
}

func TestEstimatorFreezeAndConcurrentReads(t *testing.T) {
	input := estimatorFixture(t, func(i *EstimatorInput) {
		i.Requirements.Requirements[0].Facets = &Facets{Languages: []string{"go"}}
		i.Imports[0].Observations[0].Facets = &Facets{Languages: []string{"go"}}
		c := estimatorCopy(i.Candidates[0])
		c.ID = "b"
		c.RuntimeBindingID = "home-b"
		i.Candidates = append(i.Candidates, c)
		i.Fingerprints = append(i.Fingerprints, CandidateFingerprint{"b", estimatorCopy(i.Fingerprints[0].Fingerprint)})
	})
	e, estimates, p := estimatorOutput(t, input)
	if len(e.View().Results) != 1 || len(p.Groups) != 1 || !reflect.DeepEqual(p.Groups[0].CandidateIDs, []string{"a", "b"}) {
		t.Fatal("duplicate homes not distributed", p)
	}
	pristine := e.Input()
	ref := estimatorRefs(pristine)
	input.Imports[0].Observations[0].Subject.EngineProfile.Name = "changed"
	input.Candidates[0].EngineProfile.KVContextTokens = estBP(1)
	input.Fingerprints[0].Fingerprint.EngineProfile.Quantization = "changed"
	input.Requirements.Requirements[0].Facets.Languages[0] = "changed"
	view := e.View()
	view.Results[0].Contributions[0].Weight = 0
	view.Candidates[0].EngineProfile.Name = "changed"
	*view.Results[0].Score = 0
	req := e.Requirements()
	*req.Items[0].WeightBP = 1
	req.Items[0].Facets["language"] = "changed"
	estimates[0].ContributingRecordIDs[0] = "changed"
	*estimates[0].Fitness = 0
	*estimates[0].Coverage = 0
	p.Groups[0].CandidateIDs[0] = "changed"
	expected, err := e.Estimate(e.Requirements(), pristine.Candidates, ref)
	if err != nil {
		t.Fatal(err)
	}
	if *expected[0].Fitness != 9000 || !reflect.DeepEqual(expected[0].Fitness, expected[1].Fitness) || e.View().Candidates[0].EngineProfile.Name == "changed" {
		t.Fatal("mutable alias escaped")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				got, err := e.Estimate(e.Requirements(), pristine.Candidates, ref)
				if err != nil || !estimatorEqual(got, expected) {
					t.Error("concurrent estimate drift", err)
				}
				p, err := e.Partition(got)
				if err != nil || len(p.Groups) != 1 {
					t.Error("concurrent partition drift", err)
				}
				if got[0].Fitness != nil {
					*got[0].Fitness = 1
				}
				_ = e.View()
				_ = e.Input()
				_ = e.EvaluatorVersions()
			}
		}()
	}
	wg.Wait()
	for _, field := range []string{"project", "time"} {
		t.Run(field, func(t *testing.T) {
			changed := estimatorCopy(pristine)
			if field == "project" {
				changed.Requirements.Project = "different"
			} else {
				changed.EvaluatedAt = "2026-10-02T00:00:01Z"
			}
			other, _, _ := estimatorOutput(t, changed)
			if other.InputDigest() == e.InputDigest() || other.View().ID == e.View().ID {
				t.Fatal("binding unchanged")
			}
			_, err := NewFitnessEstimator(changed, e.View())
			evalError(t, err, EstimatorViewMismatch)
		})
	}
}

func TestEstimatorPartitionLabelsAndBins(t *testing.T) {
	cases := []struct {
		name    string
		width   int64
		fitness []int64
		unknown bool
		labels  []string
		config  string
	}{
		{"equal", 1, []int64{6000, 6000}, false, []string{"fitness-band:a7ce053f279377d9218df7139d0c97d21bffcf916ac3a0c877f418959731bf4c", "fitness-band:a7ce053f279377d9218df7139d0c97d21bffcf916ac3a0c877f418959731bf4c"}, "sha256:60f42d9a9500dad6e4c336153c489b40493aec8fb781d759636a6ffe8861a73a"},
		{"one-bp", 1, []int64{6000, 6001}, false, []string{"fitness-band:a7ce053f279377d9218df7139d0c97d21bffcf916ac3a0c877f418959731bf4c", "fitness-band:5fd2b53b9a9eb5d36ae4d1c3f46f03a3d8e504f84be689262ab4f315d0d29713"}, "sha256:60f42d9a9500dad6e4c336153c489b40493aec8fb781d759636a6ffe8861a73a"},
		{"terminal", 1, []int64{10000}, false, []string{"fitness-band:133ba417497953b192c4af31c408a6706967510dc1c81aa34c311ce3ac9a6331"}, "sha256:60f42d9a9500dad6e4c336153c489b40493aec8fb781d759636a6ffe8861a73a"},
		{"unknown", 1, []int64{0, 0}, true, []string{"singleton:a", "singleton:b"}, "sha256:60f42d9a9500dad6e4c336153c489b40493aec8fb781d759636a6ffe8861a73a"},
		{"no-chaining", 250, []int64{249, 250, 499, 500}, false, []string{"fitness-band:162e63bc3a5e53ddf260a7bcb4c649e4ea5196d147c8205771fcb028ab88c37c", "fitness-band:cbf2811fc300f18f7aa996c7cb637a02bc1e0ce01098108b089109edd1100c0e", "fitness-band:cbf2811fc300f18f7aa996c7cb637a02bc1e0ce01098108b089109edd1100c0e", "fitness-band:bb55d4b20be9c18124b90e6ba9fdd551e97065af4a46b8aaa5b36b7e06330aac"}, "sha256:1fb369fc55aecb2c366e07618df119640deb315a9f60ca329d245a6510dd2b28"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := estimatorFixture(t, func(i *EstimatorInput) {
				i.Config.FitnessBandWidthBP = tc.width
				i.Registry.Models = []RegistryModel{}
				i.Candidates = []routing.ExecutionCandidate{}
				i.Fingerprints = []CandidateFingerprint{}
				observation := estimatorCopy(i.Imports[0].Observations[0])
				candidate := estimatorCopy(estimatorFixtureCandidate())
				fingerprint := estimatorCopy(observation.Subject)
				i.Imports[0].Observations = []Observation{}
				i.Imports[0].Notes = []Note{}
				for j, fitness := range tc.fitness {
					id := string(rune('a' + j))
					model := "model-" + id
					i.Registry.Models = append(i.Registry.Models, RegistryModel{model, []string{"high"}})
					c := estimatorCopy(candidate)
					c.ID = id
					c.Model = model
					c.RuntimeBindingID = "home-" + id
					i.Candidates = append(i.Candidates, c)
					f := estimatorCopy(fingerprint)
					f.ModelID = model
					i.Fingerprints = append(i.Fingerprints, CandidateFingerprint{id, f})
					if tc.unknown {
						continue
					}
					o := estimatorCopy(observation)
					o.Subject = f
					o.Provenance.OriginRef = "origin-" + id
					if fitness >= 5000 {
						o.Value = float64(fitness-5000) / 5000
						i.Imports[0].Observations = append(i.Imports[0].Observations, o)
					} else {
						// Average one measured value with n pinned -1 notes to get
						// utility F/5000-1, retaining direct empirical coverage.
						notes := (5000+fitness-1)/fitness - 1
						o.Value = float64((notes+1)*fitness-5000) / 5000
						i.Imports[0].Observations = append(i.Imports[0].Observations, o)
						for k := int64(0); k < notes; k++ {
							n := estimatorNote()
							n.Subject.ModelID = model
							n.Claim.Polarity = "weakness"
							n.Basis = "review-outcomes"
							n.Claim.Statement = fmt.Sprintf("Synthetic weakness %d", k)
							i.Imports[0].Notes = append(i.Imports[0].Notes, n)
						}
					}
				}
			})
			e, estimates, _ := estimatorOutput(t, input)
			p, err := e.Partition(estimates)
			if err != nil {
				t.Fatal(err)
			}
			if e.ConfigDigest() != tc.config {
				t.Fatalf("config %s", e.ConfigDigest())
			}
			labels := map[string]string{}
			for _, g := range p.Groups {
				for _, id := range g.CandidateIDs {
					labels[id] = g.ID
				}
			}
			for j, want := range tc.labels {
				id := string(rune('a' + j))
				if !tc.unknown && (estimates[j].Fitness == nil || *estimates[j].Fitness != tc.fitness[j] || *estimates[j].Coverage != 10000) {
					t.Fatalf("%s: estimate=%+v want F=%d C=10000", id, estimates[j], tc.fitness[j])
				}
				if labels[id] != want {
					t.Fatalf("%s: %s want %s", id, labels[id], want)
				}
			}
		})
	}
	t.Run("empty", func(t *testing.T) {
		input := estimatorFixture(t, func(i *EstimatorInput) {
			i.Candidates = []routing.ExecutionCandidate{}
			i.Fingerprints = []CandidateFingerprint{}
		})
		_, estimates, p := estimatorOutput(t, input)
		if estimates == nil || p.Groups == nil || len(p.Groups) != 0 {
			t.Fatal(p)
		}
	})
	t.Run("default-materialized", func(t *testing.T) {
		input := estimatorFixture(t, nil)
		e, _, _ := estimatorOutput(t, input)
		input.Config = EstimatorConfig{}
		other, _, _ := estimatorOutput(t, input)
		if other.InputDigest() != e.InputDigest() {
			t.Fatal("default widths not materialized")
		}
	})
}

// Keep fitness equal while measured category weight crosses coverage boundaries.
// Config digests and labels are pinned from an independent canonical-JSON SHA-256 oracle.
func TestEstimatorPartitionCoverageLabelsAndBins(t *testing.T) {
	coverage := []int64{249, 250, 499, 500, 10000}
	cases := []struct {
		name                        string
		fitnessWidth, coverageWidth int64
		buckets                     []int64
		config                      string
		labels                      []string
	}{
		{"coverage-exact", 1, 1, []int64{249, 250, 499, 500, 10000}, "sha256:60f42d9a9500dad6e4c336153c489b40493aec8fb781d759636a6ffe8861a73a", []string{"fitness-band:d713a50058497ad9d2378fad65fac8deecec23dec0ddb5b1e6ff0fb065166c36", "fitness-band:6300ca3a6de5731633ca45c47e223bbf665b9d9a5d3cabbfc0146dfd9d575a10", "fitness-band:8ee472431e4e9494a19bbdaba4475b11ef2851aba26ed1acb0fca993193451bb", "fitness-band:fa07f2af041123633a9592776d16a78912cd0214052f4c69ab3e1d5d389961ae", "fitness-band:8ca951d7505d19150e29f42aeae8e8c5808f092db1b4e8ce5a2234dbc8995064"}},
		{"coverage-width-250", 1, 250, []int64{0, 1, 1, 2, 40}, "sha256:585b521705bb256f467152b0d90b239bdafb78c4959bc6baadc479a9d6c8603c", []string{"fitness-band:4234ba63d8aadab763658600d9ff79a378d32a714da7c01a9634f6a16be817ab", "fitness-band:925be95e1ffeb222776aae6528ad728ad258cc2b2b8449d6102aec84893a2a9e", "fitness-band:925be95e1ffeb222776aae6528ad728ad258cc2b2b8449d6102aec84893a2a9e", "fitness-band:26572b441fbdeb53f00219ff8a43541083ffe31575858f8c2ee754732be71449", "fitness-band:b3cdb4c1605ff3c3066a852e0f1cfc5c0ff67fc10e2b0875a92bf3426919c8ea"}},
		{"independent-fitness-width-400", 400, 250, []int64{0, 1, 1, 2, 40}, "sha256:9256ec250b1d467d68923bb8beebec3e2000430c2eecc5850efb86eeae89de39", []string{"fitness-band:01e0588a34aa153a565e1d5c8ce4714e4adad20fd10230e408bfbe874694df6e", "fitness-band:b61b6c3b25337eb9c3aa0b6965f8d09e065d71ea1cc42810796b47ab7fb29355", "fitness-band:b61b6c3b25337eb9c3aa0b6965f8d09e065d71ea1cc42810796b47ab7fb29355", "fitness-band:275afbd1fa8fb6f2112141c5535e33110de3a3fe22ec3af991a5efca41255661", "fitness-band:993b0fe887320e5b1d7d328138af69a49e28feebe69e22c7f494ec1aaa0a9100"}},
		{"independent-coverage-width-400", 250, 400, []int64{0, 0, 1, 1, 25}, "sha256:0d11cf08172073b1a11596c0484845c64cb8d2b8b6f41cfb6b8529c43f6fc68c", []string{"fitness-band:615649c0199da38eb14299b5dd6d168b479b0a2e8e663609ec8ad63ae576de68", "fitness-band:615649c0199da38eb14299b5dd6d168b479b0a2e8e663609ec8ad63ae576de68", "fitness-band:f104117bf37870446676eac86e733ce200d5a92ea10418599ed443f653fdeca2", "fitness-band:f104117bf37870446676eac86e733ce200d5a92ea10418599ed443f653fdeca2", "fitness-band:e94a5d1de64df66f59a9dcebfb3953245f139d3e5e8b426c4a5ab5afac380631"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := estimatorFixture(t, func(i *EstimatorInput) {
				i.Config.FitnessBandWidthBP = tc.fitnessWidth
				i.Config.CoverageBandWidthBP = tc.coverageWidth
				// Cumulative weights give C=249,250,499,500,10000. All
				// supported categories have utility 0.8, so F remains 9000.
				categories := []string{"code.fix", "code.implement", "code.refactor", "code.test", "review.code"}
				weights := []float64{249, 1, 249, 1, 9500}
				i.Requirements.Requirements = []Requirement{}
				i.Imports[0].Benchmarks[0].Categories = []CategoryCoverage{}
				for k, category := range categories {
					i.Requirements.Requirements = append(i.Requirements.Requirements, Requirement{Category: category, Weight: weights[k]})
					i.Imports[0].Benchmarks[0].Categories = append(i.Imports[0].Benchmarks[0].Categories, CategoryCoverage{category, "synthetic"})
				}
				i.Derivation.Normalisations[0].Categories = categories
				candidate := estimatorCopy(i.Candidates[0])
				fingerprint := estimatorCopy(i.Fingerprints[0].Fingerprint)
				observation := estimatorCopy(i.Imports[0].Observations[0])
				i.Registry.Models = []RegistryModel{}
				i.Candidates = []routing.ExecutionCandidate{}
				i.Fingerprints = []CandidateFingerprint{}
				i.Imports[0].Observations = []Observation{}
				for j := range coverage {
					id := string(rune('a' + j))
					model := "model-" + id
					i.Registry.Models = append(i.Registry.Models, RegistryModel{model, []string{"high"}})
					c := estimatorCopy(candidate)
					c.ID, c.Model, c.RuntimeBindingID = id, model, "home-"+id
					i.Candidates = append(i.Candidates, c)
					f := estimatorCopy(fingerprint)
					f.ModelID = model
					i.Fingerprints = append(i.Fingerprints, CandidateFingerprint{id, f})
					o := estimatorCopy(observation)
					o.Subject = f
					o.Categories = categories[:j+1]
					o.Provenance.OriginRef = "origin-" + id
					i.Imports[0].Observations = append(i.Imports[0].Observations, o)
				}
			})
			e, estimates, partition := estimatorOutput(t, input)
			if e.ConfigDigest() != tc.config {
				t.Fatalf("config digest=%s want %s", e.ConfigDigest(), tc.config)
			}
			wantMembers := map[string][]string{}
			for j, wantCoverage := range coverage {
				id := string(rune('a' + j))
				v := estimates[j]
				if v.CandidateID != id || v.Fitness == nil || *v.Fitness != 9000 || v.Coverage == nil || *v.Coverage != wantCoverage {
					t.Fatalf("%s: estimate=%+v want F=9000 C=%d", id, v, wantCoverage)
				}
				if wantCoverage/tc.coverageWidth != tc.buckets[j] {
					t.Fatalf("%s: invalid expected coverage bucket %d", id, tc.buckets[j])
				}
				wantMembers[tc.labels[j]] = append(wantMembers[tc.labels[j]], id)
			}
			if len(partition.Groups) != len(wantMembers) {
				t.Fatalf("groups=%d want %d; partition=%+v", len(partition.Groups), len(wantMembers), partition)
			}
			for j, group := range partition.Groups {
				if !reflect.DeepEqual(group.CandidateIDs, wantMembers[group.ID]) {
					t.Fatalf("group %s members=%v want %v; expected coverage buckets=%v", group.ID, group.CandidateIDs, wantMembers[group.ID], tc.buckets)
				}
				if j > 0 && partition.Groups[j-1].ID >= group.ID {
					t.Fatal("groups are not in canonical order")
				}
			}
		})
	}
}

func estimatorFixtureCandidate() routing.ExecutionCandidate {
	return routing.ExecutionCandidate{ID: "a", RuntimeBindingID: "home-a", Runtime: "runtime-a", Model: "model-a", Effort: "high", ContextProfile: routing.ContextProfile{Name: "synthetic", LockSHA256: canonical.Unknown}, Transport: "synthetic", ProviderID: "synthetic", ToolProfile: "tools-a", ExecutionMode: "batch", BillingClass: routing.BillingMetered}
}

func TestEstimatorBoundaryVerification(t *testing.T) {
	for _, tc := range []struct {
		name          string
		weight, score float64
		want          *int64
		code          string
	}{
		{"upper-tolerance", 1.0000000000005, 1.0000000000005, estBP(10000), ""},
		{"upper-outside", 1.000000000002, 1.000000000002, nil, EstimatorViewMismatch},
		{"lower-tolerance", 1.0000000000005, -1.0000000000005, estBP(0), ""},
		{"lower-outside", 1.000000000002, -1.000000000002, nil, EstimatorViewMismatch},
		{"inconsistent-score", 0.8, 0.79, nil, EstimatorViewMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := estimatorOutput(t, estimatorFixture(t, nil))
			e.view.Results[0].Contributions[0].Weight = tc.weight
			e.view.Results[0].Score = &tc.score
			if tc.score < 0 {
				e.view.Results[0].Contributions[0].Direction = "-"
			}
			estimates, err := e.project()
			if tc.code != "" {
				evalError(t, err, tc.code)
			} else if err != nil || !reflect.DeepEqual(estimates[0].Fitness, tc.want) {
				t.Fatalf("%+v %v", estimates, err)
			}
		})
	}
	for _, decimal := range []string{"1e-7", "0.2001", "-1", "1.0000000000005"} {
		t.Run(fmt.Sprintf("exact-%s", decimal), func(t *testing.T) {
			r, err := estimatorRational(json.Number(decimal))
			if err != nil || r == nil {
				t.Fatal(err)
			}
		})
	}
}
