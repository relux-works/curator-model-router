package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/relux-works/curator-model-router/internal/cmrio"
	"github.com/relux-works/curator-model-router/internal/quota"
	"github.com/relux-works/curator-model-router/pkg/recommend"
	"github.com/relux-works/skill-agents-management/pkg/providerquota"
	"github.com/relux-works/skill-agents-management/pkg/vendorplugin/benchdata"
)

type repeatedStrings []string

func (s *repeatedStrings) String() string     { return strings.Join(*s, ",") }
func (s *repeatedStrings) Set(v string) error { *s = append(*s, v); return nil }

type recommendOptions struct {
	policyLoader                                                                                            func() (recommend.Policy, error)
	ctx                                                                                                     context.Context
	preflightTimeout, advisoryTimeout                                                                       time.Duration
	admission                                                                                               *recommend.AdmissionContext
	loadedPolicy                                                                                            *recommend.Policy
	role, taskClass, difficulty, language, platform, agent, model, effort                                   string
	catalog, catalogOverlay, policy, candidates, host, story, producerFamily, mode, budget, localCapability string
	delicate, fanout, refresh, json                                                                         bool
	exclude                                                                                                 repeatedStrings
}

func recommendFlagSet(name string, spawn bool, o *recommendOptions) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	for _, x := range []struct {
		name   string
		target *string
	}{
		{"role", &o.role}, {"task-class", &o.taskClass}, {"difficulty", &o.difficulty}, {"language", &o.language},
		{"agent", &o.agent}, {"model", &o.model}, {"reasoning-effort", &o.effort},
		{"local-capability", &o.localCapability}, {"catalog", &o.catalog}, {"catalog-overlay", &o.catalogOverlay}, {"policy", &o.policy}, {"candidates", &o.candidates},
		{"budget", &o.budget}, {"platform", &o.platform}, {"host", &o.host}, {"story", &o.story}, {"producer-family", &o.producerFamily},
	} {
		f.StringVar(x.target, x.name, "", "")
	}
	f.DurationVar(&o.preflightTimeout, "preflight-timeout", 0, "")
	if spawn {
		f.DurationVar(&o.advisoryTimeout, "advisory-timeout", time.Minute, "")
	}
	f.BoolVar(&o.delicate, "delicate", false, "")
	f.BoolVar(&o.fanout, "fanout", false, "")
	f.BoolVar(&o.refresh, "refresh", false, "")
	f.BoolVar(&o.json, "json", false, "")
	f.Var(&o.exclude, "exclude", "")
	if spawn {
		f.StringVar(&o.mode, "mode", "", "")
	}
	return f
}

func recommendFlags(name string, args []string, spawn bool) (recommendOptions, error) {
	var o recommendOptions
	f := recommendFlagSet(name, spawn, &o)
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return o, &Refusal{Code: "cmr_invalid_arguments", Message: "invalid " + name + " flags"}
	}
	invalidTimeout := false
	f.Visit(func(option *flag.Flag) {
		if option.Name == "preflight-timeout" && o.preflightTimeout <= 0 || option.Name == "advisory-timeout" && o.advisoryTimeout <= 0 {
			invalidTimeout = true
		}
	})
	if invalidTimeout || o.preflightTimeout < 0 || o.preflightTimeout > 24*time.Hour || o.advisoryTimeout < 0 || o.advisoryTimeout > 24*time.Hour {
		return o, &Refusal{Code: "cmr_invalid_arguments", Message: "timeouts must be positive and at most 24h"}
	}
	if o.budget != "" && o.budget != "economy" && o.budget != "balanced" && o.budget != "burn" {
		return o, &Refusal{Code: "cmr_invalid_arguments", Message: "budget must be economy, balanced or burn"}
	}
	return o, nil
}
func registryModels() []benchdata.Model {
	models := append(benchdata.OpenAIModels(), benchdata.AnthropicModels()...)
	models = append(models, benchdata.AlibabaModels()...)
	models = append(models, benchdata.GoogleModels()...)
	models = append(models, benchdata.MuseModels()...)
	return models
}

// Resolve registry aliases for matching only; forwarded argv remains untouched.
func canonicalModel(model string) string {
	for _, fact := range registryModels() {
		if fact.ID == model && fact.AliasOf != "" {
			return fact.AliasOf
		}
	}
	return model
}
func recommendation(o recommendOptions) (recommend.DecisionRecord, error) {
	o.model = canonicalModel(o.model)
	if o.platform == "" {
		o.platform = runtime.GOOS
	}
	task := recommend.TaskProfile{Platform: o.platform, Role: o.role, TaskClass: o.taskClass, OriginalTaskClass: o.taskClass, Difficulty: o.difficulty, Language: o.language}
	if o.delicate {
		task.Sensitivity = "delicate"
	}
	if o.fanout {
		task.Pipeline = "fanout"
	}
	task, err := task.Normalize()
	if err != nil {
		return recommend.DecisionRecord{}, err
	}
	catalog, err := cmrio.LoadCatalog(o.catalog)
	if err != nil {
		return recommend.DecisionRecord{}, err
	}
	var policy recommend.Policy
	if o.loadedPolicy != nil {
		policy = *o.loadedPolicy
	} else {
		policy, err = cmrio.LoadPolicy(o.policy)
		if err != nil {
			return recommend.DecisionRecord{}, err
		}
	}
	var overlay *recommend.CatalogOverlay
	overlayPath := policy.CatalogOverlay
	if o.catalogOverlay != "" {
		overlayPath = o.catalogOverlay
	}
	if overlayPath != "" {
		raw, readErr := os.ReadFile(overlayPath)
		if readErr != nil {
			return recommend.DecisionRecord{}, &recommend.Refusal{Code: recommend.InvalidOverlay, Message: "cannot read catalog overlay"}
		}
		loaded, loadErr := recommend.LoadOverlay(raw)
		if loadErr != nil {
			return recommend.DecisionRecord{}, loadErr
		}
		// Refuse unknown rows before admission discovery or optional refresh.
		if _, mergeErr := recommend.MergeCatalog(catalog, loaded); mergeErr != nil {
			return recommend.DecisionRecord{}, mergeErr
		}
		overlay = &loaded
	}
	if o.budget != "" {
		policy.BudgetMode = o.budget
	}
	if o.host == "" {
		o.host = policy.Host
	}
	if o.host == "" {
		host, err := os.Hostname()
		if err != nil {
			return recommend.DecisionRecord{}, &Refusal{Code: "cmr_host_unavailable", Message: "cannot determine host"}
		}
		o.host = strings.SplitN(host, ".", 2)[0]
	}
	var localBundle *recommend.LocalCapabilityBundle
	requestVersion := ""
	if o.localCapability != "" {
		raw, err := os.ReadFile(o.localCapability)
		if err != nil {
			return recommend.DecisionRecord{}, &recommend.Refusal{Code: recommend.InvalidLocalCapability, Message: "cannot read local capability document"}
		}
		bundle, err := recommend.FreezeLocalCapability(raw)
		if err != nil {
			return recommend.DecisionRecord{}, err
		}
		localBundle = &bundle
		requestVersion = recommend.LocalRequestVersion
	}
	for _, row := range catalog.Rows {
		if row.WeightsID != "" || row.ExpectedWeightsID != "" || row.Reasoning != nil {
			requestVersion = recommend.LocalRequestVersion
		}
	}
	ctx := o.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := o.preflightTimeout
	if timeout == 0 && policy.PreflightTimeoutSeconds != 0 {
		timeout = time.Duration(policy.PreflightTimeoutSeconds) * time.Second
	}
	candidates, source, err := cmrio.DiscoverWithOptions(ctx, timeout, o.candidates, o.role, o.agent, catalog, o.admission)
	if err != nil {
		return recommend.DecisionRecord{}, err
	}

	for _, c := range candidates {
		if c.WeightsID != "" || c.ExpectedWeightsID != "" || c.Reasoning != nil {
			requestVersion = recommend.LocalRequestVersion
		}
	}
	root := cmrio.StateRoot()
	env := os.Environ()
	if o.refresh {
		seen := map[string]bool{}
		for _, c := range candidates {
			if o.agent != "" && c.Runtime != o.agent || o.model != "" && c.Model != o.model || o.effort != "" && c.Effort != o.effort {
				continue
			}
			if seen[c.Runtime] {
				continue
			}
			seen[c.Runtime] = true
			if _, err := quota.System(c.Runtime); err != nil {
				continue
			}
			if _, err := quota.Refresh(ctx, root, c.Runtime, env, time.Now().UTC(), 10*time.Minute); err != nil {
				return recommend.DecisionRecord{}, err
			}
		}
	}
	// Freeze the routing clock once, after optional reads finish.
	asOf := time.Now().UTC()
	records, err := quota.Read(root, env, asOf)
	if err != nil {
		return recommend.DecisionRecord{}, err
	}
	record, err := recommend.BuildDecision(recommend.Request{SchemaVersion: requestVersion, LocalCapability: localBundle, Host: o.host, Story: o.story, ProducerFamily: o.producerFamily, Exclude: o.exclude,
		Admission: o.admission, Catalog: catalog, CatalogOverlay: overlay, Policy: policy, Task: task, Candidates: candidates, AdmissionSource: source,
		Locks: recommend.Locks{Agent: o.agent, Model: o.model, Effort: o.effort}, Usage: quota.Project(records, catalog, asOf)})
	if err != nil {
		return record, err
	}
	if err := ctx.Err(); err != nil {
		return record, &Refusal{Code: "advisory_timeout", Message: "advisory recommendation timed out"}
	}
	if err = cmrio.SaveDecision(root, record); err != nil {
		return record, &Refusal{Code: "cmr_decision_write_failed", Message: "cannot store decision"}
	}
	return record, nil
}
func commandError(err error, asJSON bool, stdout, stderr io.Writer) int {
	var cli *Refusal
	if errors.As(err, &cli) {
		return writeRefusal(cli, asJSON, stdout, stderr)
	}
	var rec *recommend.Refusal
	if errors.As(err, &rec) {
		return writeRefusal(&Refusal{Code: rec.Code, Message: rec.Message}, asJSON, stdout, stderr)
	}
	var quotaError *providerquota.Refusal
	if errors.As(err, &quotaError) {
		return writeRefusal(&Refusal{Code: "cmr_usage_" + quotaError.Reason, Message: "provider usage operation refused: " + quotaError.Reason}, asJSON, stdout, stderr)
	}
	if errors.Is(err, providerquota.ErrLocked) {
		return writeRefusal(&Refusal{Code: "cmr_usage_locked", Message: "provider usage refresh is locked; retry"}, asJSON, stdout, stderr)
	}
	return writeRefusal(&Refusal{Code: "cmr_usage_failed", Message: "provider usage operation failed"}, asJSON, stdout, stderr)
}
func spawnFlags(c recommend.Candidate) []string {
	flags := []string{"--agent", c.Runtime, "--model", c.Model}
	if c.Effort != "none" {
		flags = append(flags, "--reasoning-effort", c.Effort)
	}
	return flags
}

type recommendOutput struct {
	recommend.Recommendation
	AdmissionSource string     `json:"admission_source"`
	Flags           []string   `json:"flags"`
	FanoutFlags     [][]string `json:"fanout_flags"`
	Commands        [][]string `json:"commands,omitempty"`
	Mode            string     `json:"mode,omitempty"`
}

func outputRecommendation(record recommend.DecisionRecord, asJSON bool, commands [][]string, mode string, stdout io.Writer) error {
	r := record.Recommendation
	out := recommendOutput{Recommendation: r, AdmissionSource: record.Inputs.AdmissionSource, Flags: []string{}, FanoutFlags: [][]string{}, Commands: commands, Mode: mode}
	if r.Selected != nil {
		out.Flags = spawnFlags(*r.Selected)
	}
	for _, c := range r.FanOut {
		flags := spawnFlags(c)
		// Several launches against one element need parallel runs, as in cmr spawn.
		if len(r.FanOut) > 1 {
			flags = append(flags, "--allow-parallel")
		}
		out.FanoutFlags = append(out.FanoutFlags, flags)
	}
	if asJSON {
		return json.NewEncoder(stdout).Encode(out)
	}
	if _, err := fmt.Fprintf(stdout, "%sadmission=%s\n", r.RenderHuman(), out.AdmissionSource); err != nil {
		return err
	}
	for i, x := range r.Alternatives {
		quality, cost, headroom := "unknown", "unknown", "unknown"
		if x.Quality != nil {
			quality = fmt.Sprint(x.Quality.Value)
		}
		if x.Cost.USDPerTask != nil {
			cost = fmt.Sprintf("$%g/task", *x.Cost.USDPerTask)
		} else if x.Cost.TokensPerTask != nil {
			cost = fmt.Sprintf("%d tokens/task", *x.Cost.TokensPerTask)
		}
		if x.Headroom != nil && x.Headroom.Headroom != nil {
			headroom = fmt.Sprint(*x.Headroom.Headroom)
		}
		if _, err := fmt.Fprintf(stdout, "alternative=%d %s/%s/%s tier=%s quality=%s cost=%s headroom=%s reasons=%v\n", i+1, x.Candidate.Runtime, x.Candidate.Model, x.Candidate.Effort, x.Tier, quality, cost, headroom, x.ReasonCodes); err != nil {
			return err
		}
	}
	for _, cmd := range commands {
		if _, err := fmt.Fprintln(stdout, shellLine(cmd)); err != nil {
			return err
		}
	}
	if len(commands) == 0 {
		for _, flags := range out.FanoutFlags {
			if _, err := fmt.Fprintln(stdout, "task-board spawn", shellLine(flags)); err != nil {
				return err
			}
		}
	}
	return nil
}
func shellLine(args []string) string {
	out := make([]string, len(args))
	for i, s := range args {
		out[i] = "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
	}
	return strings.Join(out, " ")
}
func runRecommend(args []string, stdout, stderr io.Writer) int {
	o, err := recommendFlags("recommend", args, false)
	if err != nil {
		return commandError(err, hasJSON(args), stdout, stderr)
	}
	record, err := recommendation(o)
	if err != nil {
		return commandError(err, o.json, stdout, stderr)
	}
	if err = outputRecommendation(record, o.json, nil, "", stdout); err != nil {
		return writeRefusal(&Refusal{Code: "cmr_output_failed", Message: "could not write recommendation"}, o.json, stdout, stderr)
	}
	if record.Recommendation.Refusal != nil {
		return 2
	}
	return 0
}
