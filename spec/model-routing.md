# Task-aware model routing: specification

Status: **DRAFT** (decided design, pre-implementation). Written 2026-09-22 from the proposal "самостоятельный эпик — Task-aware Model Routing" and its enriched copy, which the operator keeps as Russian working notes outside the repositories, aligned with `skill-project-management/.specs/drafts/launch-profiles.md` (*LP*, decisions LP-D1…D16) and `curator-network-profiles/spec/network-profiles.md` (*NP*). Working names are marked *proposed*.

> **Notes 2026-09-23.** The CandidateResolver (R2) is the agent-selection resolver (`skill-project-management/.specs/drafts/agent-selection.md` §7, §9): slices A0/A1 land with roadmap milestone M4, slices B–E in M8 (`spec/track.md`). Candidates are filtered by role needs from playbooks (hard requirements) and ranked with role preferences as priors; operator and project policies are separate layers with their own digests in the decision. The library gets its own public repository `curator-model-router`.

> **Notes 2026-10-01.** The operator decided on 2026-09-30 (an operator ruling):
>
> - **D1, the split is facts, decision, enforcement.**
>   - agents-management holds the facts: the model registry and its bench data, the reactive limit plane, the machine catalog, and the new usage records (`pkg/providerquota`).
>   - This repository holds the decision: evidence, assessment, fitness, strategies and decision records.
>   - task-board keeps admission, the CandidateResolver and enforcement, budgets included.
> - **D2, no CodexBar dependency.** Usage comes from each harness's own public surfaces.
> - **D3/D4, automation.** A dedicated orchestrator implements this with maximal automation and brings only genuine product forks to the operator.
>
> Two specifications are new:
>
> - `spec/evidence-format.md`: the internal, source-agnostic evidence format, with task categories, exact effort, internal notes per model and a derived role-suitability view. R4 points there.
> - `spec/headroom.md`: usage facts per `(runtime, managed home)`, where stale means unknown, and the `headroom-aware` strategy. R2 and R6 point there.
>
> `spec/track.md` splits delivery into parallel workstreams W0–W5.

---

## 0. Goal

The orchestrator passes a description of the work and its constraints. The router selects a launch configuration from the **already admitted** candidates, records why, and hands the result to the existing spawn mechanism. It is not a proxy in front of a model API, not a scheduler and not another agent inside spawn: it is a separate, inspectable launch-preparation step.

The unit of choice is a **runtime binding + model + effort** (LP-D1), together with the Curator context profile (LP-D4), the engine profile for local models (LP-D7) and, later, an AgentOffer from the pool. A decision belongs to one launch or attempt; per-request model switching inside an agent loop is out of scope. The first release works with today's local candidates; the remote pool widens the set later and is not a prerequisite. Intelligence does not require an LLM call: matching task requirements to measurements already gives a useful choice; Jev or a structured LLM are optional, replaceable assessors.

---

## 1. What exists (verified) and what this builds on

| Existing point | Use |
| --- | --- |
| `internal/spawn/workload_class.go`; workload recommendations in `spawn-preflight` with a content-bound snapshot digest and rationale flags (`--recommendation-snapshot-digest`, `--recommendation-rationale`, `--selection-rationale`) | deterministic baseline of requirements per role and task class; the shape of "decision bound to a snapshot" already exists |
| Admission: ceilings v3/v4 admitted pairs, `providerlimits` suppression, schedule windows, frozen-policy retry (`spawnruntime/limit_retry.go`) | the router never re-implements them; it consumes the admitted set |
| Module `pkg/plugin.Registry` is kind-agnostic; `vendorplugin/bench.go` carries Bug Hunt measured/interpolated/cost observations; scores re-anchored on Bug Hunt Bench in `v0.5.12` | first evidence source; a new plugin kind needs no second framework |
| LP: machine catalog `runtimes.toml` with `bound_on_this_machine`, `spawn.context_profile`, billing class per binding, availability block keyed by `(runtime, managed home)`, engine status from `curator-inference-manager` | inputs of the CandidateResolver (R2) |
| `pkg/providerquota` in agents-management: designed and reviewed under the provider-quota design (2026-09-08), not yet built | the usage records of `spec/headroom.md`; built as workstream W3 |
| `cmd/workload_projection.go`, `validateSpawnWorkloadSelection`, `spawnruntime.WorkloadSelection` | named in the proposal from a source snapshot; verify at the start of slice A |

---

## 2. Decisions

### R1 Architectural place: a library with caller adapters

A Go module with the working name **`curator-model-router`** (its own repository since 2026-09-24; never a network service). It imports neither task-board, nor the module's exec paths, nor ax storage, and creates no processes; callers convert their structures into independent DTOs. A shared resolver does not mean one global policy: task-board keeps its role/project constraints, `curator-run` keeps machine locks and environment constraints; the router copies none of them.

```text
Task / work description / role
        │
        ▼
 Caller adapter (task-board | curator-run)
        │
   ┌────┴──────────────────────────┐
   ▼                               ▼
TaskEnvelope             CandidateResolver
                         registry, policy, locks, catalog binding,
                         context profile, availability, engine status
   │                               │
   │                       EligibleCandidates
   └────────────┬──────────────────┘
                ▼
          curator-model-router:  assess → estimate → select
                ▲
          EvidenceSnapshot (benchmarks, own outcomes)
                │
                ▼
          RoutingDecision
                │
                ▼
     existing spawn gate: revalidate → build → launch
```

Owners: task-board (task, role, project constraints, routing mode, launch and acceptance); agents-management (identities and compatibility of runtime/model/effort/engine, plugin graph, launch contracts, the machine catalog); `curator-model-router` (evidence, requirements, fitness, strategies, decision records); `curator-run` (composition of a task-aware choice with the environment and launch pipeline); `curator-inference-manager` (engine profiles, status and capacity; `ensure` is called by the process owner, never by the router); process host / future coordinator (real launch, resources, reservations, cancellation); Curator (delivery of pinned packages and managed homes; the account boundary).

### R2 Four questions, four owners

| Question | Who answers | Inputs |
| --- | --- | --- |
| Allowed? | operator-declared policy | ceilings and admitted pairs; `bound_on_this_machine` (an unbound runtime is excluded before scoring, distinct from `no_eligible_candidates`); project `spawn.schedule` and catalog `windows`; billing admit; data policy of the project |
| Technically compatible? | module registry + catalog | tools, effort vocabulary, context window, transport, engine profile, execution mode |
| Fit for the work? | the router's estimator | requirements ↔ measurements or explicitly labelled estimates |
| Available now? | the preflight availability block (LP-D9) | inventory/auth, quota, reactive limits, engine status as two facts (`ready`: serving or lingering; `ensurable`: profile and weights present, not quarantined, host arbitration would admit an `ensure`) and leases, all keyed by `(runtime, managed home)` |

The estimator answers the third question only. A positive score never overrides the other three. Prediction is not enforcement: "this will fit the budget" enforces nothing; the execution owner applies limits. No eligible candidates returns `no_eligible_candidates` **before** any paid assessor runs. A cold but ensurable engine keeps its candidate eligible and carries a start cost (R6); only `not ensurable` excludes it. Otherwise a stopped engine would never be chosen and therefore never ensured (LP-D9, amended 2026-09-22).

Usage (quota) is advisory. The usage record per `(runtime, managed home)` orders candidates through the `headroom-aware` strategy and never admits or removes one; a stale record counts as unknown (`spec/headroom.md`). The reactive limit plane stays the authority on `Limited`.

### R3 Data model

Six objects: `TaskEnvelope` (the delegated work: description, substantive revision, role, criteria, context, tools; a concrete task, never the whole Epic or the orchestrator's conversation), `ExecutionCandidate`, `CandidateSnapshot` (the admitted set and how it was obtained), `EvaluationSnapshot` (measurements, rubric, assessments, estimates, evaluator versions), `RoutingPolicy` (what may be chosen, how to compare, what to do with insufficient data), `RoutingDecision`.

`ExecutionCandidate` is a configuration that can really be launched:

```text
runtime binding id + model + effort            (LP-D1)
Curator context profile {name, lock_sha256}    (LP-D4)
transport and provider id from runtimes.toml    (LP-D3)
engine profile from engines.toml (local models) (LP-D7)
network profile, resolved by the caller       (NP-N4 precedence)
tool profile / execution mode
AgentOffer, compatible EnvironmentBinding       (later, pool)
```

Execution fingerprint for evidence matching: vendor/model identity, harness version, effort, engine profile (engine kind, weight digest, quantization, `kv_context_tokens`, `prefill_chunk_tokens`; later the KV cache type, slice K4), environment/tool profile, execution profile. For a cloud alias the exact internal version is recorded as unknown, never faked as a pin. Private members (endpoints with credentials, paths, PIDs) never enter a fingerprint: it is built from the public `RuntimeProvenance` (LP-D11). The work digest does not change on every `ready → running` transition; content and criteria are one thing, current executability is the workflow's.

### R4 Evidence

Sources in order: an adapter over the existing Bug Hunt observations, a JSON importer for own eval runs, and (after feedback) outcomes of ordinary runs. Storage unit:

```text
Observation
    execution_fingerprint
    benchmark / dataset / split
    evaluation_protocol_digest
    metric / unit / direction
    value / sample_count / uncertainty
    evidence_kind      measured | interpolated | transferred
    grading_method     tests | exact-match | llm-judge | human
    provenance / raw_artifact_ref
    observed_at
```

`evidence_kind` and `grading_method` are independent: an LLM-judge result can be a real observation without becoming a test-oracle result. Semantics: no measurement is *unknown*, not zero; no price is *unknown*, not free; an unknown model in a file is an *unresolved observation*, not a runtime registration. Leaderboards are never averaged; the estimator states the mapping from requirements to measurements, normalization and applicability; reprints of one result are not independent confirmations; a measurement at one effort is never presented as a measurement at another, and an estimate derived for another effort exists only as `evidence_kind = transferred` with its own uncertainty and a stated transfer rule. An import creates a new snapshot and never rewrites the module's global `Rank.Score`.

The full format is `spec/evidence-format.md` (2026-10-01): benchmark definitions with versions and protocols, the task-category taxonomy, exact effort per observation, internal notes per model, and the derived role-suitability view.

### R5 Evaluator contracts

Five narrow interfaces instead of `Evaluator(any) → float`: `EvidenceSource` (import observations), `TaskAssessor` (work → requirements and features), `FitnessEstimator` (requirements + candidates + evidence → fitness), `SelectionStrategy` (frozen estimates + preferences → choice or abstain), `OutcomeEvaluator` (result and checks → a quality observation, never the right to accept work). Running a benchmark is a separate optional capability, never hidden inside an importer.

The first `TaskAssessor` is deterministic: existing workload derivation plus explicit task metadata (role, language, change type, expected context, tools). A rubric-based assessor comes next, over a shared `JudgmentBackend` (Jev with typed Choice/Score/Noul, or a structured LLM) chosen **explicitly**, so the router never picks the model that picks the router's model. Classifier confidence is not the probability the agent succeeds; confidence, evidence coverage and empirical calibration are different fields. Logical stages are not five paid calls: requirements in one bounded call, fitness computed locally.

### R6 Selection

Relative utility, not a claimed success probability. Order: eligible and compatible → sufficient quality and evidence coverage → cost/latency/quality preference → stable tie-break → choice or abstain, so a low price never compensates arbitrarily poor quality. Strategies: `config-order` (today's behaviour as baseline), `quality-first`, `cost-with-quality-floor` (the floor cites the rubric/estimator version; `0.9` without a scale means nothing). `headroom-aware` (2026-10-01) wraps any of these: inside an interchangeable group it prefers capacity that would expire at reset, conserves subscriptions burning faster than their window, keeps a reserve for orchestrators and reviewers, and spreads in-flight runs; it never trades quality outside the group (`spec/headroom.md` §5). `calibrated_success_probability` is a later, separately validated type. Cost is not one number: API billing estimate, quota consumption (providerquota per managed home), latency, local engine occupancy from `curator-inference-manager status`, the start cost of a cold but ensurable engine; the catalog's `billing` class (`local | subscription | metered`, LP-D9) selects which of these applies. **The selector is a pure function** over frozen inputs: no network, no files, no clock, no model call, so replay reproduces the decision even when the assessor was nondeterministic.

### R7 Modes, defaults, manual choice

| Mode | Behaviour |
| --- | --- |
| `off` (legacy) | everything as today |
| `shadow` | the decision is recorded, the effective choice is unchanged |
| `recommend` | the caller receives a recommendation and chooses explicitly |
| `select` | the caller lets the router fill the allowed fields |

Locks and explicit values are never replaced: with `--agent` and `--model` given, the router may pick effort only, and only when allowed. `select` needs an explicit free-field mask naming the candidate members that may differ: runtime, model and effort on request, and the dependent settings network profile and context profile only when explicitly freed. The CandidateResolver first resolves every candidate's dependent settings (binding default → project default → operator default, NP-N4; context profile per LP-D4). It fixes each explicitly requested value across all candidates and, before scoring, excludes a candidate whose resolved dependent setting differs from a fixed or non-freed value, with `candidate_dependent_setting_mismatch`. The router chooses among whole variants and edits no member; the process owner re-resolves and applies the chosen variant. Choosing a binding whose defaults imply another egress is therefore a permitted choice of a whole variant when the mask frees the network member, never a route switch by the router (NP-N13). Soft role defaults become the baseline, not the final answer. `abstain`, `no_eligible_candidates`, timeout and an invalid response are distinct outcomes. By default `select` launches nothing when there is no decision; an explicit baseline fallback is allowed for named reasons only, is recorded as `origin: baseline_after_abstain`, and can never override a mandatory evidence floor.

### R8 RoutingDecision and validity

```text
RoutingDecision
    decision_id / schema_version
    task_envelope_digest / role_context_digest
    candidate_snapshot_digest / evaluation_snapshot_digest / routing_policy_digest
    assessor / estimator / selector versions
    selected_candidate {runtime, model, effort, context_profile, network_profile?, engine_profile?}
    alternatives, reason_codes, evidence_refs
    selection_origin, applicability_scope, prepared_at / expires_at
```

Human-readable explanation is built from structured reasons; free evaluator text is never the authority for runtime/model/effort. A digest binds content, not authorship; trusted local storage suffices for the MVP, authenticated delivery or a signature is needed across machines. `decision_id` is content-addressed, or a reference carries the decision digest beside the id. Either way, a reference from a signed assignment names one immutable record and binds it to the concrete execution parameters the assignment carries. Two kinds of change: **substantive** (task, role, policy, model identity, tool profile, context profile lock, network profile ref) makes the decision stale; **operational** (heartbeat, quota, slot) requires a fresh availability check, not byte-equality of a volatile snapshot. A candidate that became unavailable returns `selected_candidate_unavailable`; re-selection is a new operation reusing stored estimates for unchanged configurations. Validation therefore compares member-wise: substantive members against the stored decision by digest, operational members through a fresh availability read; one digest over the whole preflight is never compared. The router reserves no resources and keeps no second quota counter.

### R9 task-board integration

- First refactoring: extract one `CandidateResolver` from the workload projection and make `config-order` the baseline strategy; query, routing preparation and spawn validation share one path for ceilings, schedules, provider limits, catalog binding and availability. The machine half (catalog, `bound_on_this_machine` in preflight, dependent-setting resolution) depends on LP Phase 1 and is slice A1; slice A0 extracts the resolver and reproduces `config-order` without it.
- Preparation is an explicit operation; `project_config` never becomes a hidden Jev call. CLI (*proposed*): `task-board routing prepare TASK_ID --role R --policy P --json`, `routing explain DECISION_ID`, `routing replay DECISION_ID`. `prepare` creates no worktree and starts no sub-agent; with an external assessor it is an explicitly paid operation.
- The orchestrator receives the selected candidate and `decision_id`, then passes explicit parameters to the ordinary spawn: `--agent <runtime-id> --model <id> --reasoning-effort <e> --context-profile <name>` and, when the decision carries one, `--network-profile <ref>` (LP-D12 vocabulary; no `--profile`), plus `--routing-decision <id>` (*proposed*) which binds the decision the way `--recommendation-snapshot-digest` binds a recommendation today. A convenience `spawn --route` may compose the same steps later and must not be a second router.
- Two validation phases: early (format, authorship, policy binding, selected candidate) before any side effect; after the ordinary task read (substantive revision and context). Mismatch refuses; no LLM call inside spawn. Existing ordering, claims and cleanup are preserved.
- Effective configuration proof: the run manifest (`RuntimeProvenance` + Curator profile lock, LP-D11) is compared with the decision. For codex the `-c model_provider` overrides and for claude the owned env literals are part of the module's plan, hence applied by construction; native args, `-c` overrides after `--` or a harness auto-mode must not silently change the model after validation. An adapter that cannot confirm effective configuration may support `recommend` but must not claim strict `select`.

### R10 Curator launcher

A separate task-aware mode: a work description or a prepared decision reference plus a selection profile. Environment, locks and explicit values resolve first; the router receives the remaining set; then the ordinary `BuildLaunchWithEnvironment` and the separate availability check follow. task-board does not launch through `curator-run` (LP-D5); both consumers use the library through their own adapters.

### R11 Network profiles, engines, pool

`curator-model-router` decides what to launch; `curator-network-profiles` decides the allowed route (NP-N13). The router may treat compatibility with a mandatory network profile as a constraint; it never switches VPNs, credentials or quota domains; a network failure is not model-quality evidence. Engines: the router may weigh `engine.profile` capacity as compatibility and cost; `curator-inference-manager ensure` is the process owner's preflight action, never the router's. Pool (later): `ExecutionOption = ExecutionCandidate + AgentOffer + compatible EnvironmentBinding`; the inference machine and the tools machine differ, so an Xcode requirement is checked on the execution environment, not on the worker; the coordinator confirms placement and reserves; reviewer independence rules apply before scoring.

### R12 Retry and feedback

Provider-limit retry keeps its frozen policy; in the first integration the routing decision covers the initial attempt explicitly and later attempts record their actual configuration and the link to the decision. Hard constraints always hold. A strict policy incompatible with old retry refuses or forbids candidate change, and a new snapshot, assessor or objective is never mixed in between attempts silently. Quality escalation after a bad result is a workflow decision, not a provider-limit retry. Feedback comes from the result, not the agent's self-report: `decision_id`, `run_id/attempt_id`, `actual_execution_fingerprint` (later including engine profile and the KV cache type), `kv_outcome` `warm|restored|cold` (`warm`: a retained live slot was reused; `restored`: a snapshot was restored; `cold`: a full prefill), or `unknown` with a reason code when it cannot be measured, always measured from the usage of the run's attributed main request, never from the last request seen (LP-D10; KV track Q9, 2026-10-01), `artifact_revision`, checks/review/acceptance, actual usage/latency/resources, `failure_class` (model error, network, auth/limit, environment, `engine_ensure_failed`, cancellation, incomplete evaluation are distinct; unknown stays unknown). `OutcomeEvaluator` forms an observation, and acceptance stays with the workflow. New outcomes enter the next snapshot and never change the active strategy after each task; trained estimators or calibration artifacts ship as separately reviewed versions.

### R13 Cost, privacy, extensions

The router must be cheaper than the value of its choice: policy bounds the task projection size, external calls, time and cost; small tasks use rules and cached assessments. Caches split by `TaskAssessment` (task + rubric + backend), `CandidateEstimates` (assessment + candidates + evidence), `Selection` (estimates + preferences); live availability is never cached as permanent permission; caches respect project boundaries and data policy. First plugin path: trusted Go components in the composition root; external extensions later through a bounded, versioned stdio RPC. The registry validates the dependency graph, not plugin safety: permissions, secrets and network are granted by the host, never by an evaluator manifest.

---

## 3. Phases and acceptance

| Slice | Delivers | Value |
| --- | --- | --- |
| A0. Baseline and contracts | shared resolver, `ConfigOrderStrategy`, DTOs, `RoutingDecision`, regression tests (legacy parity), the contract appendix of §5; no machine half | today's choice reproduced and explained through one path |
| A1. Machine half | catalog, `bound_on_this_machine`, dependent-setting resolution and the mismatch reason code | candidates are real runtime bindings of this machine |
| B. Evidence-driven recommendations | Bug Hunt adapter, JSON importer, snapshots, rule assessor, deterministic estimator | work- and measurement-dependent recommendations without a mandatory LLM |
| C. Semantic assessment | rubric, Jev/LLM backends, budgets, privacy, shadow mode | richer task assessment without changing the effective choice |
| D. Opt-in selection at spawn | two-phase gate, provenance, caller adapters, actual-attempt feedback | the orchestrator really delegates the choice |
| E. Empirical improvement | eval sets, calibration, controlled experiments; later the pool adapter | improvement proven on own tasks |

C is not a dependency of D. First production profiles are enabled for bounded task classes and candidates. Dependencies on LP: A0 starts now; A1 starts after LP Phase 1 (catalog); slice B runs beside LP Phase 3 and consumes its availability block as the single "available now" source, including the `ready`/`ensurable` engine facts.

Readiness checks: routing `off` preserves defaults, admission, admitted non-recommended choices and retry; a new score never admits a forbidden model and never replaces locks or explicit values; unknown, interpolation and transfer are never presented as direct measurement; stored inputs replay the choice without network or model; a different task/policy/configuration is rejected while an ordinary heartbeat creates no false stale; query never calls an assessor, prepare never starts a worker, selector never reserves; the selected configuration is really applied including native overrides; abstain, timeout, malformed response and no-candidates are distinct with explicit fallback; feedback is bound to the actual attempt and verified revision; a new source or assessor changes neither spawn nor the registry core. Shadow mode shows behaviour, not superiority: value is measured on replay/holdout tasks or bounded experiments with equal criteria and budget, by accepted-result quality, full cost to acceptance including routing and retries, time, coverage and abstain rate; learning only from the history of chosen models is a biased sample.

---

## 4. Rules and non-goals

- One plugin graph, one admission path, separate evaluation, pure selector, recorded decision, existing spawn.
- Never a process from the router; never a reservation; never a hidden LLM call in a local query; never a score that widens admission; never a free-text authority for runtime/model/effort.
- Vocabulary: runtime binding, not "model string"; `--agent`/`--runtime`, `--context-profile`, `--network-profile`; never `--profile`.

## 5. Contract appendix (deliverable of slice A0)

The object shapes are in R3 and R8; the exact comparison and replay rules are written with the code as a short appendix with test vectors. Decided now:

| Area | Rule |
| --- | --- |
| Digest | canonical JSON with sorted keys and an explicit schema version, SHA-256 over those bytes; defaults are materialized before hashing; collections are ordered by a declared key, never by insertion or map order |
| Unknown | an absent field means unknown; the canonical form carries no `null`; zero is a measured value; a required field without a value carries an explicit `unknown` marker |
| Replay | the three snapshots, the policy and the assessor/estimator/selector versions reproduce the decision with no network, file, clock or model access |
| Tie-break | stable comparison order, then candidate id lexical; never map iteration |
| Spawn validation | substantive members (task, role, policy, model identity, tool profile, context-profile lock, network profile ref) against the stored decision; operational members (heartbeat, quota, slot, engine readiness, network profile digest) through a fresh availability read |

## 6. Closed decisions and remaining choices

> **Note 2026-10-02.** [spec/slice-b.md](slice-b.md) specifies slice B's deterministic estimator, C2 partition, C-quality-cost strategies and internal `evalrun` review set; its seven operator forks use reversible defaults in shadow/recommend only.

Closed by this document: the router is a library with caller adapters; candidates are runtime bindings with the LP fingerprint members; the four-question split and its owners; pure selector; four modes with a free-field mask; decision document shape; two-phase validation in task-board; dependency on LP Phase 1 for the machine half. Added 2026-09-22 after the independent review: dependent settings (network profile, context profile) are resolved by the caller and fixed unless the mask frees them, so the router never switches a route; engine availability is two facts, `ready` and `ensurable`, and a cold but ensurable engine stays eligible with a start cost; cross-effort evidence exists only as `transferred`; validation compares member-wise; slice A is split into A0 (now) and A1 (after LP Phase 1); the contract appendix of §5 is an A0 deliverable. After the second review: `decision_id` is content-addressed or referenced together with its digest, so a signed assignment binds one immutable decision to the execution parameters it carries.

Added 2026-10-01 from the operator decisions of 2026-09-30:

- The facts, decision and enforcement split (D1): usage records are module facts, and the router consumes them.
- No CodexBar (D2).
- The evidence format is `spec/evidence-format.md`. It supersedes the module placement proposed in the task-board evidence-store design.
- Headroom is a soft ordering inside interchangeable groups, and stale means unknown (`spec/headroom.md`).
- Delivery runs as parallel workstreams (`spec/track.md`).

Added 2026-10-01 from the KV-cache track (an operator ruling; wiki `research/kv-cache-orchestration/ARCHITECTURE.md`, Q9 settled with the LP owner):

- R12's KV outcome is `warm|restored|cold`, or `unknown` with a reason code, measured from the attributed main request's usage. LP-D10 is amended to the same wording on the LP side.
- KV state never admits and never refuses a candidate, by the principle of LP-D9 and R2: a missing or failed KV state is a reported cold prefill. Its router terms are slice K4 in `spec/track.md`, which waits for K2a telemetry.

Remaining for the implementing session (record why when choosing): the first judgment backend (Jev vs structured LLM), the initial evidence sources beyond Bug Hunt, and the `--routing-decision` flag name.
