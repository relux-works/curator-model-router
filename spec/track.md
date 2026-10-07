# Track: model routing

Status: **DRAFT**, 2026-09-23, updated 2026-10-01.

**Specifications:**

- `spec/model-routing.md`: decisions R1–R13, with the independent review folded in;
- `spec/evidence-format.md`: the evidence format;
- `spec/headroom.md`: usage facts and the headroom-aware strategy.

**Recommend:** [spec/recommend.md](recommend.md) defines the plug-and-play task-aware selector. `pkg/recommend` supplies the pure catalog/policy engine, explanations and replay; the command adapter supplies admission, frozen usage and spawn integration.

**Roadmap:**

- slices A0 and A1 belong to milestone M4. The task-board half of A0 is the agent-selection resolver; the router half is W0 and W1.
- Availability per profile, which the usage records of W3 feed, is M5b.
- Slices B to E are M8.
- Slice K4, the router terms of the KV-cache track, is M8, after K2a (M5b).

## Why

Today a model is chosen for a task from a static preference list per workload class. The router chooses among already admitted candidates from evidence and from remaining subscription capacity, and it records why. It never admits anything, never launches anything, and never switches accounts or egress.

## How it fits agent selection

The agent-selection resolver (`skill-project-management/.specs/drafts/agent-selection.md` §7) is the router's CandidateResolver.

- Its hard filters answer "allowed", "compatible" and "available". Its ranking step is where a routing strategy runs.
- Role needs from playbooks are the router's requirement features. Role preferences are priors. Hard requirements are never violated.
- Evidence is kept per role or requirement class and per execution fingerprint.

## Workstreams (2026-10-01)

The operator decisions of 2026-09-30 split delivery into workstreams that can run in parallel. Each has one owner and its own repository surface, and they share only the contracts written in the specifications.

| Workstream | Repository | Delivers | Needs | Runs |
| --- | --- | --- | --- | --- |
| **W0** scaffold and shared contracts | curator-model-router | Go module; CI (`go vet`, `go test -race`, actionlint); package layout; `pkg/canonical` with the digest part of the §5 appendix and its test vectors; the shared contract types listed below; `cmd/cmr` stub | none | first, small |
| **W1** router core | curator-model-router | over W0's contract types: the `config-order` and `headroom-aware` strategies as pure functions; decision records with `explain` and `replay`; the test vectors of `headroom.md` §8 | W0 | beside W2 and W3 |
| **W2** evidence | curator-model-router | the format of `evidence-format.md` in `pkg/evidence`; the store; the `native`, `bughunt` and `notes` importers; the role-suitability derivation; `cmr evidence`, `cmr note`, `cmr suitability` | W0 | beside W1 and W3 |
| **W3** usage records | skill-agents-management | `pkg/providerquota`, which is the provider-quota design Lane 1 (the usage-records workstream and its six tasks) with two amendments: a Muse reader over MSP `usage/read` in place of `not_supported`, and no CodexBar. Ends at a module PR merged to main. | none: its contract is `headroom.md` §3 | from the start, beside W1 and W2 |
| **W4** seams with task-board | curator-model-router and skill-project-management | a design, no code: the A0 contract appendix; `routing prepare/explain/replay`; `--routing-decision`; how the preflight `quota` block and the in-flight counts become `CandidateSnapshot` facts; the outcome feedback of R12; push from hosted sessions | W1 contracts | backlog |
| **W5** integration | skill-project-management, plus the module tag | the A0/A1 resolver; the provider-quota design Lane 2 (executor, `provider_quota()`, the preflight `quota` block); the router adapter in `shadow`, then `recommend` | the integration readiness gate, W1–W4 | backlog, after the readiness gate |

### Shared contracts, frozen before the parallel lanes

W0 lands these before W1 and W2 start, so that neither lane has to guess or wait:

1. **Canonical JSON and digest:** the rules of `model-routing.md` §5, as `pkg/canonical` with committed test vectors. This is the digest part of the A0 appendix; the task-board boundary part is W4.
2. **The usage projection:** the `CandidateSnapshot` usage members of `headroom.md` §3.1, including `as_of`, the versioned scope map and the in-flight counts, as Go types in `pkg/routing`. W1 reads only this; W3 produces only the record of `headroom.md` §3; the mapping from one to the other is W4 and W5.
3. **The DTO skeletons of R3 and R8** in `pkg/routing`: `TaskEnvelope`, `ExecutionCandidate`, `CandidateSnapshot`, `EvaluationSnapshot`, `RoutingPolicy` with its headroom parameters, and `RoutingDecision`.
4. **The estimation seam:** an `Estimate` type (per candidate: fitness, coverage, contributing record ids, estimator version) and the `FitnessEstimator` interface, both in `pkg/routing`. W1's strategies need no estimator (`config-order` and `headroom-aware` over it). W2 implements an estimator over the role-suitability view in slice B, after both lanes land.
5. **Evidence references:** an `EvaluationSnapshot` names its evidence snapshot by digest, and its estimates cite record ids (`evidence-format.md` §2.0).

A change to a frozen contract after W0 is its own reviewed PR, announced to the other lanes before it lands.

### Package ownership inside curator-model-router

W1 and W2 run at the same time without colliding, because each owns its own packages:

| Package or file | Owner |
| --- | --- |
| `pkg/canonical` | W0 |
| `pkg/routing`: the contract types | W0, then changed only as a frozen contract |
| `pkg/routing`: strategies, decision records, `explain`, `replay` | W1 |
| `pkg/evidence` | W2 |
| `cmd/cmr` | split by subcommand file |

W1 never imports `pkg/evidence`, and W2 never imports the strategies.

## Slices

The slices of `model-routing.md` §3, mapped to workstreams:

| Slice | Delivers | Workstreams | When |
| --- | --- | --- | --- |
| A0 | one resolver, `config-order` parity, decision record, contract appendix | router half: W0 and W1; task-board half: W4 and W5 | M4 (with agent selection S1) |
| A1 | machine half: catalog bindings, `bound_on_this_machine`, dependent settings | W5 | M4 |
| B | evidence snapshots (Bug Hunt adapter, own evals), rule assessor, deterministic estimator; headroom-aware ordering in shadow mode; normative design: [spec/slice-b.md](slice-b.md) | W2, then W1 and W5 | M8, after M5b availability |
| C | semantic assessment in shadow mode (Jev or a structured LLM, chosen explicitly) | later | M8 |
| D | opt-in selection at spawn, two-phase validation, effective-configuration proof | later, needs W5 | M8 |
| E | calibration and measured gain on own tasks | later | M8 |
| K4 | KV-cache routing terms: a `kv_state`/`kv_tokens` operational fact read from availability and never an admission input (no `ensure`, no reservation); a prefill cost term for `local` candidates; `kv_outcome` feedback (`warm\|restored\|cold`, or `unknown` with a reason code, from the attributed main request's usage); the KV cache type in the execution fingerprint. No code until K2a telemetry exists. | router (W1, then W5) | M8, after K2a telemetry |

## Repository

`curator-model-router` was created on 2026-09-24. It stays private until the operator decides its visibility; public is proposed. It is consumed by task-board and by the public `curator-run`.
