# Slice B: deterministic fitness and internal review evidence

Status: **DRAFT**, 2026-10-02. This is the normative design for slice B of
`spec/track.md`, issues #13 and #14. It specifies future contracts and behavior;
it does not claim that they are implemented.

**Inputs:** R4–R8 of `model-routing.md`, `evidence-format.md` §4 and §6,
`headroom.md` §5, `contract-appendix.md`, and `decision-bundle.md`. This design
adopts the supplied 2026-10-02 research report's #13, #14 and
“Cross-issue algorithm: exact #13 semantics” sections. Product defaults are
listed in “Pending operator decisions” below.

The W0 contracts stay frozen until their separate contract PRs land. This
document specifies C2 and C-quality-cost for those PRs. New selection behavior
belongs to `router-quality-v1`. `router-core-v1` keeps its historical behavior
and refusals so old bundles still replay.

## 0. Scope and owners

Slice B computes relative utility over already admitted whole configurations.
Fitness is not a probability of success. Coverage is not classifier confidence.
Neither changes admission, acceptance, budgets or launch enforcement.

| Part | Owner and dependency |
| --- | --- |
| Estimator, view verification and estimation replay | `pkg/evidence`, using routing DTOs; routing never imports evidence |
| C2 partition types and validation | Separate frozen-contract PR in `pkg/routing` |
| C-quality-cost policy, costs, versions and reasons | Separate frozen-contract PR in `pkg/routing` |
| Quality strategies and headroom composition | Routing implementation against landed contracts |
| `evalrun`, review protocol and derivation `2` | Evidence implementation; no routing contract change |
| Files, clock freeze and bundle assembly | Caller or CLI composition root |
| Eval execution and cost forecasts | Host commands; neither importer nor selector runs them |
| Task-board preparation, retention and launch validation | W4 design, then W5 integration |

Use `shadow` first, then `recommend`. Pending defaults authorize no `select`
rollout. Offline development does not wait for live quota data. Live rollout
still follows M5b availability and the W4/W5 gates in `track.md`.

## 1. Immutable estimator inputs

Keep the frozen interface:

```text
Estimate(Requirements, []ExecutionCandidate, EvidenceSnapshotRef)
    -> ([]Estimate, error)
```

The implementation is `ViewEstimator`, constructed by `NewFitnessEstimator`.
Its evidence-owned `EstimatorInput` contains these exact members:

| Member | Meaning |
| --- | --- |
| `schema_version` | `fitness-input-v1` |
| `snapshot` | Exact immutable evidence snapshot |
| `imports` | Complete imports named by the snapshot, ordered by digest |
| `registry` | Exact registry bound by the snapshot |
| `requirements` | Project/role requirements and category mapping |
| `derivation` | Exact normalisations, note weights, discounts and version |
| `evaluated_at` | Frozen time that determines note staleness |
| `candidates` | Admitted variants, ordered by candidate id |
| `fingerprints` | One `{candidate_id, fingerprint}` per candidate, ordered by id |
| `config` | Versioned estimator configuration below |

```text
config
    schema_version          fitness-config-v1
    version                 suitability-bp-v1
    fitness_band_width_bp   integer 1..10000; default 1
    coverage_band_width_bp  integer 1..10000; default 1
```

Materialize widths before hashing. They are independent of headroom's
quota-slack `band_width_bp`. The fitness scale is `role-utility-bp-v1`.

The constructor validates snapshot, import and registry identities, requirements
and derivation. It deep-copies mutable values and calls W2 `Derive`. A stored
view must be regenerated and compared by canonical content. A view id alone
does not prove derivation. No constructor or method reads files, a clock, the
current-store pointer or a network. Return independent copies, including
pointers and slices.

Fingerprint bindings cover candidates exactly. Model, runtime, exact effort,
engine profile and tool profile agree with the admitted variant wherever they
overlap. `harness_version` and `execution_profile` come only from frozen public
caller facts. Do not infer harness version from runtime or equate
`execution_mode` with `execution_profile`. Unknown optional members stay
omitted. Several homes may share a fingerprint: derive it once and distribute
its result to every candidate id.

The routing requirement projection is explicit. Sorted categories have positive
integer `weight_bp` summing to 10000. Scalar facet keys are `language`,
`platform`, `context_size`, `horizon` and `role`. Reject a facet that cannot be
represented losslessly. A caller may apportion positive source weights by exact
largest remainder before freezing both documents: floor each exact share,
distribute residual bp by descending remainder, and break ties by lexical
category. A resulting zero-weight category is invalid.

`Estimate` first compares `EvidenceSnapshotRef.Digest` with the held snapshot
digest. A difference returns the existing typed `routing.Error`, code
`estimator_snapshot_mismatch`, using `NewEstimatorSnapshotMismatchError`.
Requirements and full candidates must then equal the held inputs.
`record_ids` is a declared reference set, not a hidden sub-snapshot filter. It
is sorted, contains valid observation/note addresses in the held snapshot, and
includes every actual contribution. Extra inapplicable addresses are allowed.
Missing contribution references refuse the call.

Return exactly one `Estimate` per candidate, sorted by `candidate_id`. It has
`estimator_version: suitability-bp-v1`, optional integer `fitness` and
`coverage`, and required `contributing_record_ids`. Both numbers are 0..10000.
Omission differs from zero.

| Evidence-local refusal | Trigger |
| --- | --- |
| `estimator_input_mismatch` | Held requirements/candidates or bindings differ |
| `estimator_requirements_invalid` | Invalid weights or lossy scalar facets |
| `estimator_view_mismatch` | View content, score or scale cannot be verified |
| `estimator_record_ref_mismatch` | Invalid references or missing contribution ids |
| `estimator_config_invalid` | Unsupported config/version or invalid widths |
| `estimator_partition_input_mismatch` | Partition requested from estimates other than the estimator's own output |

Reuse canonical and evidence identity refusals for their existing meanings.
The new evidence-local refusals are typed evidence errors. The frozen
digest-mismatch case specifically requires `routing.Error`.

## 2. W2 view, fitness and coverage

### 2.1 Matching and utility

Use the active set and pinned registry. Preserve W2 supersession, retraction
and conflicts. For observations, unresolved subjects and unknown or
unrecognised effort never match. Known model and effort agree exactly. A known
conflicting optional observation fingerprint member prevents a match. Any
unknown optional member makes the observation match partial, even when both
sides omit it. These fingerprint rules apply to observations only. Apply one
partial discount per contribution, not one per unknown axis. Default discount
is 0.5. Keep W2 facet applicability under the pinned derivation. Create no
cross-effort transfer.

Notes retain landed W2 `matchNote` semantics. The note's model must resolve and
equal the candidate model, and the candidate effort must be known and accepted
by the pinned registry. A declared effort list must contain that exact effort;
an omitted effort list is a partial prior against a known candidate effort.
Known runtime must agree, and a known candidate harness version must satisfy
the note's declared harness range. Unknown runtime or harness scope on either
side makes the match partial; a known conflict or unsatisfied range prevents
matching. Engine, tool and execution-profile axes are ignored for notes.
Fully scoped compatible notes are direct even when those ignored candidate
axes are unknown. Applicable facets may still make a note partial. A note
never becomes an observation and cannot match an unknown candidate effort.

Observations enter only through declared benchmark id/version/metric/category
normalisations. For higher-better use `z = clamp((value-min)/(max-min),0,1)`;
for lower-better use `1-z`. Apply the declared partial discount. Evidence kind
remains measured, interpolated or transferred. Transfers require their rule
id/version and uncertainty. `suitability-bp-v1` adds no uncertainty penalty.

A note contributes confidence times basis weight and, when partial, the
discount. Strength is positive; weakness and caution are negative.

| Confidence | Weight | Basis | Weight |
| --- | ---: | --- | ---: |
| `low` | 0.25 | `benchmark-reading` | 0.4 |
| `medium` | 0.6 | `incident` | 0.8 |
| `high` | 1 | `operator-judgement` | 0.6 |
| | | `review-outcomes` | 1 |
| | | `run-outcomes` | 0.8 |

These are pinned W2 priors. Stale notes have zero weight and establish no
support. Future notes are excluded. A date-only `review_by` includes the whole
UTC day; a timestamp expires strictly after that instant. Stale notes remain
visible with age and zero weight.

Within each candidate/category, deduplicate observations by
`(origin_ref, benchmark id/version, metric)`. Prefer direct over partial, then
lexical record id. Unknown origin uses the record address. Reprints do not
create independent confirmation. Keep `evidence_conflict` in view issues.

For each supported category, average its included signed contributions. Weight
that average by its requirement weight divided by total supported-category
weight. Include a measured zero in the count. Exclude expired and zero-weight
notes. Missing categories have `coverage:none` and no utility observation.
If all categories lack support, score is absent. Notes can change the average.
A changed prior, uncertainty penalty or benchmark weighting requires a new
reviewed derivation version.

### 2.2 Integer-bp fitness

Project frozen canonical view bytes. Parse each decimal contribution weight
as an exact rational, apply its sign and sum in declared contribution order to
obtain `u`. Use checked exact integer/rational arithmetic, not float
multiplication followed by truncation.

Verify the view and its scale `[-1,1]`. The signed sum agrees with the displayed
score within W2's existing tolerance `1e-12`. Clamp boundary overshoot within
that tolerance to -1 or +1. Larger overshoot or inconsistent content is
`estimator_view_mismatch`.

```text
score absent:  omit fitness
score present: fitness = round_half_even(5000 * (1 + u))
```

This affine utility scale maps utility 0 to 5000, 0.2 to 6000 and 0.4 to 7000.
An absent score never becomes 5000. Utility -1 maps to known fitness zero.

For nonnegative rational `n/d`, let `q` be quotient and `r` remainder. Choose
`q` if `2*r<d`, `q+1` if `2*r>d`, and the even one if equal. Use this exact
rounding for fitness and coverage.

### 2.3 Independent coverage

Coverage uses all requested category weight, including missing categories.
Read category kind and match from the verified W2 view.

| Coverage kind | Coefficient `a_k`, bp |
| --- | ---: |
| `measured` | 10000 |
| `interpolated` | 7500 |
| `transferred` | 5000 |
| `notes-only` | 0 |
| `none` | 0 |

Let `m_k` be 1 for direct, the derivation's exact partial discount for partial,
and 0 for no match. With positive weights `w_k` summing to 10000:

```text
coverage = round_half_even(sum(w_k * a_k * m_k) / 10000)
```

Round once after exact rational summation. Preserve W2's provenance preference:
measured, interpolated, transferred, then notes-only; direct breaks ties within
a kind. A measured/partial summary remains so even if interpolated/direct
support also exists. Do not maximize a coefficient separately or relabel it.

No usable evidence yields omitted `fitness`, `coverage:0` and
`contributing_record_ids:[]`. This estimator always establishes coverage for
valid nonempty requirements. Nil coverage is reserved for an evaluation that
did not establish it. Missing categories reduce coverage, not utility through
fabricated zero observations. Notes add no empirical coverage. Notes-only
qualification needs an explicit coverage floor of zero.

Contribution ids are sorted unique addresses that actually entered the view.
Include measured-zero observations. Exclude stale/future/zero-prior notes,
unresolved subjects, unmapped metrics, discarded reprints and superseded or
retracted records. Retractions explain lifecycle but are not contributions.
The bound view keeps full contribution details.

## 3. C2: optional estimator partition

Add optional `estimator_partition` to `EvaluationSnapshot`:

```text
estimator_partition
    schema_version      estimator-partition-v1
    estimator_version   suitability-bp-v1 for this estimator
    groups[]            ordered by id
        id              nonempty opaque label
        candidate_ids[] ordered lexically, unique, nonempty
```

`EstimatorPartition.Validate` checks structure and supported schema.
`ValidateAgainst(candidates, estimates)` checks membership and versions.
Groups are disjoint and exhaustive: every admitted id occurs exactly once.
Singletons are valid. `groups:[]` is valid only for an empty candidate set.
Required collections are non-nil. Every candidate has exactly one estimate.

Partition version equals every estimate's version, the `evaluator_versions`
entry `fitness-estimator`, and bundle `versions.estimator_version`. Validate
these relations at the enclosing evaluation/bundle boundary. Unknown versions
cannot support this partition.

| Refusal | Trigger |
| --- | --- |
| `headroom_partition_required` | Required fitness-band partition absent |
| `routing_invalid_estimator_partition` | Bad schema, empty group, overlapping, extra or missing membership |
| `routing_estimator_version_mismatch` | Inconsistent or unknown estimator versions |
| `routing_estimate_set_mismatch` | Candidate/estimate set mismatch after C-quality-cost lands |

Reuse `canonical_unordered`, `canonical_duplicate_key`,
`contract_missing_field` and `contract_empty_field` for structural failures.
C2 alone uses `routing_invalid_estimator_partition` for a non-bijective
estimate set. C-quality-cost adds the dedicated set-mismatch code. Hashing
never repairs groups.

`Partition(estimates)` accepts only this immutable estimator's own output,
including versions and contribution ids. For known fitness F and coverage C:

```text
fitness_bucket  = floor(F / fitness_band_width_bp)
coverage_bucket = floor(C / coverage_band_width_bp)
```

Width 1 means exact equality; F=10000 is a valid terminal bucket. Fixed bins
avoid nontransitive neighbor chaining. Unknown F or C gets its own singleton.

Known group id is `fitness-band:` plus full SHA-256 hex of canonical
`{schema_version:fitness-group-v1, estimator_version, config_digest,
fitness_bucket, coverage_bucket}`. Unknown id is `singleton:<candidate-id>`.
Sort group and member ids by lexical UTF-8 order. Labels are opaque; selectors
read member arrays. Include local, metered and below-floor alternatives too.

`fitness-band` consumes only this partition. The wrapper never rebuilds groups
from scores or quota-slack width. C2 has no floor-status member; composition
detects bands that straddle a floor.

Omit an absent partition. Never serialize `null` or synthesize an empty one.
Estimation replay regenerates and compares a stored partition; when it is
absent, replay preserves that absence and never synthesizes a partition.
Absence preserves all old evaluation canonical bytes and digests. Presence is
hashed with the evaluation and changes the bound decision id. Older strict
readers may reject the field; roll out version-aware adapters.

## 4. C-quality-cost: floor, costs and selectors

### 4.1 Contract members and validation

Add optional `quality` and `cost` to `RoutingPolicy`, and optional `costs` to
`EvaluationSnapshot`. Old defaults keep all three absent, preserving bytes and
digests. Present fields obey W0 strict-field, null, ordering and digest rules.

```text
quality
    rubric_id
    rubric_version
    estimator_version
    scale                   role-utility-bp-v1
    minimum_fitness_bp      integer 0..10000, inclusive
    minimum_coverage_bp     integer 0..10000, inclusive
cost
    version                 billing-lex-v1
    model_version           non-unknown frozen cost-model version
    billing_order[]         semantic order, not a sorted set
costs
    schema_version          routing-costs-v1
    model_version
    candidates[]            ordered by candidate_id, one row per candidate
        candidate_id
        api_usd_micros?      integer
        quota_demand_bp?     integer
        quota_unit?         required exactly when quota_demand_bp is present
        latency_ms?         integer
        engine_occupancy_ms? integer
        start_ms?            integer
        source              public Provenance: name, version, digest
```

`CandidateCost` is exactly one row of `costs.candidates`, including
`candidate_id`, every present optional value and the complete `source`.

Both new strategies require explicit `quality`. No numeric floor is mandated.
Strict decoding requires both threshold keys; explicit zero is valid. Floor
rubric id/version matches the frozen rubric. Estimator version matches
estimates, evaluation provenance and bundle options. Assessments resolve to the
same projected requirements. Reject absent, unknown, unsupported or mismatched
floor scale/version. A bare `0.9` is not a floor.

`cost-with-quality-floor` also requires `cost` and `costs`. `quality-first`
rejects a `cost` policy because its v1 comparator does not use cost.
`config-order` rejects non-nil `quality` or `cost` policy fields. Evaluation may
retain unused cost rows for comparison with a config baseline.

Cost policy and snapshot model versions are identical and non-unknown. Require
the supported schema/comparator version. Each row has public source name/version
and a strict digest. Quantities are integers in 0..10^15. Unknown rows are
present with value fields omitted. Reject extra/missing/duplicate rows and
class-inapplicable quantities. Missing is not zero. Perform no unlabelled
currency or resource conversion.

### 4.2 Qualification and total order

Let P contain exactly admitted candidates with known fitness and coverage
passing both inclusive floors under matching versions. Unknown fails even a
zero floor. Invalid estimates/versions are refusals, not evidence abstentions.
Retain every admitted candidate in the snapshot and successful order.

Empty admitted set returns `no_eligible_candidates` before assessment or
estimation. Nonempty set with empty P abstains with `quality_floor_unmet`.
Abstention has no order. Otherwise return a total permutation with consecutive
positions 0..n-1.

`quality-first` sorts P by `(-fitness, -coverage, candidate_id)`, then appends
non-P candidates by id. Config order is not a quality prior or tie-break.
This version has no cost tie-break.

`cost-with-quality-floor` uses `billing-lex-v1`. Class selects a complete tuple:

| Class | Ascending tuple | Meaning |
| --- | --- | --- |
| `metered` | `(api_usd_micros, latency_ms)` | This task's frozen API cost forecast and execution latency |
| `subscription` | `(quota_demand_bp, latency_ms)` | Additional demand under a common declared `quota_unit` |
| `local` | `(engine_occupancy_ms, start_ms, latency_ms)` | Occupancy, start cost and execution latency |

Execution latency excludes separately listed local startup. A ready engine may
have explicit `start_ms:0`; missing telemetry is unknown. Cold but ensurable
engines remain admitted. The router never calls `ensure` or reserves capacity.
KV state/tokens/prefill savings wait for K2a/K4.

Subscription demand is a forecast, not used percent, remaining balance or
headroom slack. `quota_unit` names a versioned prediction/normalisation domain.
Different units need explicit cost-model normalization to one shared unit;
otherwise they are incomparable. Credits stay display-only. Benchmark USD is
not automatically this task's API cost.

Require all tuple members. Among P:

1. No complete tuple: abstain with `cost_unknown`.
2. Complete competitors in different classes: require operator-declared
   `billing_order`, an exact permutation of `local`, `subscription`, `metered`.
   Empty `[]` supplies no cross-class preference and permits a single-class
   complete comparison. Mixed classes without priority abstain with
   `cost_incomparable`; malformed nonempty orders refuse the policy.
3. Complete subscription competitors in their cohort need equal `quota_unit`;
   otherwise abstain with `cost_incomparable`.
4. Sort complete candidates by `(class_position, class_tuple..., -fitness,
   -coverage, candidate_id)`. Single-class comparison needs no class position.
5. Append passing unknown-cost candidates by `(-fitness,-coverage,candidate_id)`,
   then non-P candidates by id.

Class priority is operator utility, not monetary commensurability. Cost never
compensates for failing a floor. Missing cost neither wins as free nor changes
admission.

### 4.3 Reasons, refusals and fallback

Add these `ReasonCode` values in C-quality-cost:

| Reason | Meaning |
| --- | --- |
| `quality_floor_applied` | Bound mandatory floor governs this decision |
| `fitness_unknown` | Omitted fitness, never zero-filled |
| `quality_below_floor` | Known fitness below minimum |
| `coverage_below_floor` | Coverage below minimum or unknown |
| `quality_floor_unmet` | No candidate passes both floors; base abstention |
| `quality_first_ordered` | Quality comparator produced the order |
| `cost_with_quality_floor_ordered` | Cost comparator produced the order after qualification |
| `cost_unknown` | Required dimensions absent; also all-unknown-cost abstention |
| `cost_incomparable` | No declared comparison between known competitors; abstention |

| New routing refusal | Trigger |
| --- | --- |
| `routing_quality_policy_required` | New strategy has no explicit floor |
| `routing_quality_version_mismatch` | Unsupported/inconsistent floor scale, rubric or estimator binding |
| `routing_estimate_set_mismatch` | Estimates do not cover candidates exactly |
| `routing_cost_policy_required` | Required cost policy or snapshot absent |
| `routing_invalid_cost` | Bad row set, provenance, unit, class field or quantity |
| `routing_cost_version_mismatch` | Unsupported/mismatched cost versions |
| `headroom_quality_floor_conflict` | Movable subscriptions mix floor pass/fail |
| `headroom_cost_knownness_conflict` | Passing movable subscriptions mix complete/unknown cost tiers |

Reuse `routing_invalid_policy`, `routing_unknown_enum`,
`routing_invalid_estimate`, C2 codes and canonical/contract structural codes.

`Route` derives mandatory-floor status from bound quality policy even if
`versions.mandatory_evidence_floor` is omitted or false. Mark quality decisions
`quality_floor_applied`. `BaselineFallback` rejects either this marker or the
existing mandatory-floor flag. Floor, cost or reserve abstention under this
policy cannot unlock fallback. No-candidates stays distinct.

Under `router-quality-v1`, BuildDecision records the strategy's decision before
fallback. An abstention remains `abstain` with `router` origin even when fallback
is authorized. Route then optionally calls BaselineFallback, a separate recorded
transition to `baseline_after_abstain` with its own content id and the original
abstention reasons. Replay mirrors the stored stage: router decisions skip
fallback; baseline-after-abstain decisions include it with the same frozen
snapshot, recorded selected variant as baseline, authorization and floor inputs.
Replay equality compares decisions at the same stage. Preserve historical
`router-core-v1` behavior.

### 4.4 Headroom composition

For `router-quality-v1`, call the base first. Pass abstention through unchanged
before requiring a partition. Preserve the old pre-base partition refusal under
`router-core-v1` for replay.

Resolve groups by the configured source: whole-configuration equality for
`same-pair-any-home`, explicit lists for `declared-groups`, C2 membership for
`fitness-band`. Never widen groups from scores.

Every movable subscription subgroup must be homogeneous in floor pass/fail.
Otherwise refuse `headroom_quality_floor_conflict`. For cost-with-floor, its
passing movable members must also share complete or unknown cost status;
otherwise refuse `headroom_cost_knownness_conflict`. Never silently drop
members, split a partition or re-bin around a floor. Local/metered members
stay fixed and do not enter these subgroup checks.

Keep subgroup base slots fixed. Sort subscriptions by the existing key
`(over, reserve, -quota_band, inflight, base_position, candidate_id)`. Keep
non-subscription and ungrouped slots fixed. Preserve all ids and base positions.
Freshness, incomplete-fact neutrality, protected roles, over, reserve and
spreading remain as in `headroom.md` §4–§5. All-reserved quantifies over the
whole admitted set, not just P.

Within declared equivalence, headroom may override subscription cost tuples
after these guards. Explain shows both orders. The composed policy does not
promise a global cost minimum and cannot promote a failing alternative into
a passing candidate's slot.

## 5. Determinism, explanation and replay

Dispatch `router-quality-v1` consistently in `Route`, `BuildDecision`, `Explain`,
`ExplainDecision` and `Replay`, including internal explanation calls. Do not
replace the old selector constant globally. Preserve old bundles, canonical
goldens, explanations and pre-B refusals under `router-core-v1`. Unsupported
versions keep `routing_selector_version_mismatch`.

Extend new-version structured explain with optional members:

| Member | Content |
| --- | --- |
| `estimate` | Exact candidate `Estimate` |
| `quality` | `{passes_floor, reason_codes[]}` |
| `cost` | `{estimate, complete, comparison_unit?}` |

Emit `cost` exactly when the evaluation contains a `CandidateCost` row for
that candidate, including an unknown-cost row or an unused baseline forecast.
`cost.estimate` is the exact bound `CandidateCost` row, preserving omitted
quantities, explicit zero and provenance without projection or conversion.
`cost.complete` is a boolean: true exactly when every member of the candidate's
billing-class tuple in §4.2 is present (including the subscription unit).
Completeness is per candidate; it does not assert comparability with another
class or subscription unit.

`cost.comparison_unit` is an optional string. Emit it exactly when
`cost.complete` is true; omit it for incomplete or wholly unknown cost, never
serialize `null`. Its value is determined by billing class:

| Class | `cost.comparison_unit` value | Interpretation |
| --- | --- | --- |
| `metered` | Literal `usd-micros` | Primary cost in USD micros; secondary latency is in ms |
| `subscription` | Exact bound `CandidateCost.quota_unit` string | Primary demand in bp under that versioned domain; secondary latency is in ms |
| `local` | Literal `milliseconds` | Occupancy, startup and execution latency are each in ms |

The unit labels the tuple's dimensions; it authorizes no conversion or
cross-class comparison.

Keep existing headroom fields and base/final positions. The bound view explains
contributions, notes, partial matches and coverage kinds. Do not describe
partial, transferred or notes-only utility as direct measurement. Human explain
renders structured values; free text has no selection power.

Keep `DecisionBundle` unchanged. Add an evidence-owned audit capsule:

```text
EstimatorReplayBundle
    schema_version  estimator-replay-v1
    input           complete EstimatorInput
    view            exact derived View
    routing         complete DecisionBundle
```

The evaluation's ordered `evaluator_versions` includes:

| Name | Binding |
| --- | --- |
| `fitness-estimator` | Algorithm version `suitability-bp-v1` |
| `fitness-input` | Complete frozen input digest |
| `role-suitability` | Derivation version and digest |
| `role-suitability-view` | View id as digest |

`ReplayEvaluation` verifies these entries, candidates against routing snapshot,
role against envelope, rubric requirements against projection, and evaluation's
evidence digest. Always regenerate view and estimates and compare their
canonical content before selection replay. If the stored evaluation includes
`estimator_partition`, regenerate it from those estimates and the frozen
config, then compare its canonical content too. If it is absent, preserve the
absence and never synthesize a partition during replay. Selection replay still
enforces the strategy's partition requirement; absence is valid for
quality-first without fitness-band headroom. Never replace stored evaluation
before comparison. Use existing `ReplayResult` and exact paths such as
`$.routing.evaluation.estimates[0].fitness`. Typed input refusals stay distinct
from replay differences.

Existing `cmr replay` proves selection equality from stored evaluation, not
estimation. Add explicit-file composition surfaces:

```text
cmr evaluation build --input INPUT --rubric FILE --json
cmr evaluation replay --input CAPSULE --decision FILE --json
```

Build emits evaluation and view; the caller assembles the routing bundle.
CLI reads explicit files once; libraries follow no artifact reference. Estimator
replay does not regenerate a host cost forecast. Archive cost-model inputs by
their bound digests separately.

W2 view construction still has binary64-derived fields. The rational projection
is exact for identical canonical view bytes. Re-estimation pins the derivation
and must reproduce its bytes on supported architectures; release vectors detect
view drift. Changed inputs or versions create new decisions, not edited records.

### 5.1 Shadow comparison and value

Pure comparison consumes original capsules/decisions and separately prepared
trial bundles. Process cases in declared order with distinct original decision
ids. Verify original estimation and selection replay first; mismatch is an error.
Trial mode is `shadow`. Trial envelope, snapshot and evaluation equal original
bytes. Only policy and supported selector/decision options may differ. Changed
evidence or estimator requires a separately prepared case.

For config baseline, copy frozen inputs, clear quality/cost policy, disable
headroom/fallback, and use shadow mode. Return complete baseline and trial
bundles/decisions with their own digests. The caller archives them. Comparison
performs no I/O or launches.

Report `cases`, `replay_equal`, `selection_changed`, `abstained`,
`unknown_fitness` and optional `mean_coverage_bp`. First four count decisions;
unknown fitness counts omitted candidate values. Mean uses present candidate
coverages and half-even rounding; omit it if none. Both no-candidates is
unchanged; trial abstention against baseline selection counts as changed.
Threshold sensitivity uses separate explicit policy batches. Invalid cases use
`shadow_invalid_case`; changed frozen inputs use `shadow_input_mismatch`.

Replay demonstrates behavior, not superiority. Predeclare a bounded holdout,
paired or randomized experiment with equal criteria/budget. Measure accepted
quality, full cost through acceptance including routing/retries, time to
acceptance, empirical coverage and abstain rate. Report denominators and missing
outcomes. Keep USD, quota and occupancy separate unless a reviewed conversion
exists. Record actual-attempt fingerprint, decision/run/attempt ids, reviewed
artifact revision and R12 failure classes. Acceptance comes from checks/review,
not self-report. Chosen-model-only logs do not establish counterfactual gain.
Until held-out results exist, gain is unknown. Feedback enters later snapshots,
not a live policy update.

## 6. `evalrun` importer

### 6.1 Input and mapping

The host executes an eval and exports JSON. The importer receives bytes, mapping
and pinned registry. It runs no eval, fetches nothing and reads no artifact.
New types are importer-owned; `evidence-import` stays unchanged.

```text
EvalRun
    schema_version  evalrun-v1
    run_id
    exported_at
    mapping_ref     {name, version}
    mapping_digest
    benchmark       existing Benchmark shape
    results[]       keyed by (result_id, metric)
        result_id
        subject
            model_name
            runtime_name?
            harness_version?
            effort?
            engine_profile?
            tool_profile?
            execution_profile?
        categories[]
        facets?         existing Facets shape
        metric
        value           finite number
        sample_count    positive integer
        uncertainty?    existing Uncertainty shape
        grading_method
        cost?           existing per-sample Cost shape
        observed_at
        raw_artifact_ref nonempty opaque reference
        supersedes[]?   observation addresses
EvalRunMapping
    schema_version  evalrun-mapping-v1
    name
    version
    models[]        {external_name, internal_id}, ordered by external_name
    runtimes[]      {external_name, internal_id}, ordered by external_name
```

Strict canonical input rules apply: no null, nonfinite values, duplicate keys,
unknown fields or trailing data. Required collections are explicit. Mapping
and result keys are unique and ordered. Run/result ids use only
`[A-Za-z0-9._-]+`. One result id identifies one fingerprint over one case set.
Multiple metrics share it and agree on subject, artifact, observation time and
sample count. Another effort/runtime uses another result id. Conflicts refuse.

Mapping name/version equals `mapping_ref`; its canonical digest equals
`mapping_digest`. Map exact names, without case folding or closest match.
A mapped model absent from pinned registry stays unresolved. An unmapped name
stays unresolved even if it resembles a registry id. A stated unmapped runtime
also makes the subject unresolved: preserve text and report `runtime_unresolved`.
Absent runtime is unknown and permits only partial matching. `Prepare` retains
and reports unknown categories.

Copy effort verbatim. Omission or `unknown` stays unknown. `none` is exact only
when explicitly stated. Preserve/report registry-unrecognised tokens; infer no
default. Unknown effort never matches, even against unknown effort.

Require exact benchmark id/version and strict known `sha256:` protocol digest.
Review-set dataset revision, split and metric definitions are explicit. Changed
protocol, prompts, scaffold, limits, dataset or grading needs a new benchmark
version. Never hash an empty placeholder protocol. Conflicting definitions at
one benchmark address refuse import.

### 6.2 Provenance and idempotence

| Output member | Value |
| --- | --- |
| `source` | `internal-evals` |
| `measured_by` | `internal` |
| `importer.name` | `evalrun` |
| `importer.version` | `1+map.` plus mapping digest's 64 hex characters |
| `imported_at` | Frozen source `exported_at` |
| `provenance.source` | `internal-evals` |
| `provenance.retrieved_at` | Frozen source `exported_at` |
| `provenance.origin_ref` | `evalrun:<run_id>:<result_id>` |
| `provenance.raw_artifact_ref` | Copied opaque reference, never read |
| `observed_at` | Measurement time, separate from export |
| `evidence_kind` | `measured` for completed aggregates |
| `grading_method` | Actual source method per observation |

This binds mapping bytes without extending `evidence-v1`. Completed results and
positive sample count are required. Runner omits cancelled/incomplete/missing
metrics and reports separate diagnostics; they never become measured zero.
This importer creates no interpolation/transfer. Explicit transfers use native
evidence with rule and uncertainty. Optional costs keep per-sample meaning and
the protocol's denominator. Whole-run USD cannot become per-sample cost.

Pass through `Prepare`, preserving its report and importer diagnostics. Same
frozen export/mapping/registry binding reproduces addresses, bytes, digests and
snapshot import set. `Store.Add` returns `already_present:true`. Relocation and
JSON whitespace change no identity. Never substitute now. A changed mapping
with stale export `mapping_ref` or `mapping_digest` is refused. A changed
mapping with matching updated export references is a valid new import, bound
to the new mapping digest and importer version. Different exports can likewise
create new imports; origin dedup still counts reprints once per benchmark/metric.
Corrections add immutable observations with `supersedes`. Same run/result id
never authorizes overwriting bytes.

| New importer refusal | Trigger |
| --- | --- |
| `evidence_evalrun_schema` | Unsupported export/mapping schema |
| `evidence_evalrun_protocol_required` | Protocol absent or unknown |
| `evidence_evalrun_mapping_mismatch` | Supplied mapping name/version or canonical digest differs from the export's `mapping_ref` or `mapping_digest` |
| `evidence_evalrun_result_conflict` | Duplicate result/metric or inconsistent shared-result facts |
| `evidence_evalrun_invalid_result` | Invalid ids, sample count or required result facts |

Reuse canonical strict codes and `evidence_invalid_time`,
`evidence_invalid_metric`, `evidence_invalid_category`, `evidence_invalid_value`,
`evidence_benchmark_conflict`, `evidence_address_mismatch`. Unknown names/effort
are successful-import diagnostics. Duplicate result keys use
`evidence_evalrun_result_conflict`; disorder uses `canonical_unordered`.

```text
cmr evidence import FILE --importer evalrun --mapping MAP --registry REGISTRY --store DIR --json
cmr suitability --derivation FILE
```

Reject `--imported-at` with evalrun and `--mapping` with other importers. Merge
importer/store reports into `ImportResult.report`, deduplicating and sorting
`(code,ref,detail)`. Runtime diagnostics survive store add. JSON errors go to
stdout with nonzero exit, as in W0.

## 7. First internal review set and derivation version 2

Initial protocol is `internal-review-set@1`, category `review.code`. It is a
protocol to collect, not completed data. Bound the class to Go code review on
our repositories. Use planted defects plus independently adjudicated ground
truth, with human resolution of ambiguous matches. Start with unweighted F1;
report severity and critical-defect performance separately.

Commit a content-addressed corpus manifest and protocol before collecting data.
Benchmark names exact revision/split and protocol digest. Include positive
cases and clean controls. Exclude holdout cases from profile tuning. Invent no
real corpus, run or model score.

| Protocol field | Required meaning |
| --- | --- |
| `benchmark_id`, `benchmark_version` | `internal-review-set`, `1` |
| `corpus_manifest_digest`, `dataset_revision`, `split` | Exact corpus/revision and declared tuning/holdout split |
| `task_class`, `categories`, `facets` | Go review class, `review.code`, language `go` and actual platforms |
| `scaffold_version`, `prompts_digest` | Runner scaffold and exact instructions |
| `allowed_context`, `allowed_tools` | Equal permitted context/tools |
| `time_limit_s`, `token_limit`, `repeat_count` | Declared limits/repeats |
| `effort_capture` | Effort as launched; unknown stays unknown |
| `grading_method`, `judge_instructions`, `judge_version` | Actual matching/adjudication and pinned judge if used |
| `finding_match_criteria`, `ambiguity_resolution` | Defect matching, duplicate handling and human adjudication |
| `case_weighting`, `severity_reporting` | Unweighted first objective; separate severity/critical-defect report |
| `metric_definitions`, `completion_rule` | Definitions below; full case set for comparable aggregates |
| `cost_denominator`, `sample_count_definition` | Per-case cost and independently evaluated case count |

Each manifest case includes `case_id`, repository revision, base/artifact
revisions, patch digest, allowed context/tools, planted or adjudicated defect
ids, expected severity and finding-match criteria. Duplicate findings never
earn repeated TP credit. Protocol states how unmatched/ambiguous findings
become FP or receive adjudication.

| Metric | Unit/direction | Quality input? |
| --- | --- | --- |
| `true_defects_found` | Count, higher-better | Diagnostic |
| `false_findings` | Count, lower-better | Diagnostic |
| `missed_defects` | Count, lower-better | Diagnostic |
| `review_f1` | Ratio 0..1, higher-better | Only mapped quality metric |
| Time/cost diagnostics | Explicit native units/denominators, lower-better | Diagnostic |

For a complete evaluated case set:

```text
review_f1 = 2*TP / (2*TP + FP + FN)
```

Zero denominator means omitted F1, not fabricated 0 or 1. Report correct clean
reviews separately. Incomplete sets are not comparable with complete ones.
`sample_count` counts independently evaluated cases, not F1 denominator or
findings. Infer no uncertainty from the ratio alone. Observation grading is
`tests`, `exact-match`, `human` or `llm-judge` as actually used; only benchmark
may say `mixed`.

`ReviewDerivationV2()` returns `role-suitability` version `2`. Preserve version
`1` bytes/behavior. Retain its Bug Hunt mapping and add exactly:

```text
benchmark_ref  {id: internal-review-set, version: 1}
metric         review_f1
min            0
max            1
direction      higher_better
categories     [review.code]
```

Diagnostics remain `observation_unmapped`; never average correlated counts into
quality. Category mapping/taxonomy versions stay unchanged: `review.code`
exists already. Select derivation `2` explicitly through `--derivation FILE`;
default stays `1`. Benchmark/protocol and derivation remain independently
replayable. New benchmark versions need declared mappings.

## 8. Acceptance vectors

Numeric cases are synthetic, not model measurements. Implementation PRs commit
canonical inputs/outputs/digests and typed refusal vectors. Normal checks only
compare goldens. An independently authored exact arithmetic oracle checks
expectations before golden updates. This documentation work runs no builds/tests.

### 8.1 Estimator

Use one required category, normalisation 0.8 and partial discount 0.5 unless
stated. Direct cases have fully specified compatible fingerprint axes; realistic
cloud unknown axes have separate partial vectors.

| Support | Utility | Fitness bp | Coverage bp |
| --- | ---: | ---: | ---: |
| Direct measured | 0.8 | 9000 | 10000 |
| Partial measured | 0.4 | 7000 | 5000 |
| Direct interpolated | 0.8 | 9000 | 7500 |
| Direct transferred with rule/uncertainty | 0.8 | 9000 | 5000 |
| Partial transferred | 0.4 | 7000 | 2500 |
| Direct measured zero | 0 | 5000 | 10000 |
| Scoped high-confidence operator strength note: exact effort listed, matching runtime/harness, compatible facets; candidate engine/tool/execution axes unknown | 0.6 | 8000 | 0 |
| Unscoped high-confidence operator strength note: effort list, runtime and harness range omitted; candidate effort known | 0.3 | 6500 | 0 |
| Effort-unscoped high-confidence operator strength note: effort list omitted, matching runtime/harness and facets; candidate effort known | 0.3 | 6500 | 0 |
| Scoped note effort list excludes candidate effort | Absent | Omitted | 0 |
| Unscoped note with unknown candidate effort | Absent | Omitted | 0 |
| High-confidence operator strength, partial | 0.3 | 6500 | 0 |
| Medium-confidence operator strength, partial | 0.18 | 5900 | 0 |
| High-confidence operator weakness, partial | -0.3 | 3500 | 0 |
| Stale/future notes only, observation with unknown effort, unknown candidate effort or no match | Absent | Omitted | 0 |
| Measured category weight 7500, missing 2500 | 0.8 | 9000 | 7500 |
| Same categories, partial measured | 0.4 | 7000 | 3750 |
| Direct measured 0.8 plus direct strength note 0.6 | 0.7 | 8500 | 10000 |

Half-even: u=0.2001 gives fitness 6000; u=0.2003 gives 6002. Partial measured
support for a 25-bp category, with 9975 bp missing, gives coverage 12. Known
utility -1 gives fitness zero; unknown stays omitted.

Also pin wrong-digest routing error type/code; missing record reference; lossy
facets; conflicting fingerprints; duplicate homes; unknown/unrecognised effort;
unmapped metrics; all note confidence/basis weights; date/timestamp expiry;
partial facets; missing transfer rule/uncertainty; reprints; supersession,
retraction and conflicts; different project/time bindings; caller mutation and
concurrent reads. Estimation replay detects substituted self-consistent views,
contributions and partitions.

### 8.2 C2

| Vector | Expected result |
| --- | --- |
| Absent partition on old evaluation | Identical bytes/digest |
| Empty candidate/estimate set, `groups:[]` | Valid |
| Equal known F/C, widths 1 | Same group |
| 1-bp difference, widths 1 | Separate groups |
| F=10000 | Valid terminal bucket |
| Unknown a and b | Separate singleton groups |
| Width 250, F=249,250,499,500 with equal C | Buckets 0,1,1,2; no chaining |
| Overlap/extra/missing member, empty group or bad schema | `routing_invalid_estimator_partition` |
| Unsorted ids or duplicate keys | Existing canonical refusal |
| Mixed estimator versions | `routing_estimator_version_mismatch` |
| Successful fitness-band base, partition absent | `headroom_partition_required` |
| Estimation replay of quality-first with no fitness-band headroom and no stored partition | Regenerated view/estimates and selection compare equal; partition stays omitted and evaluation bytes/digest are unchanged |
| Estimation replay of a successful fitness-band decision with a stored partition | Regenerate and compare the partition as well as view/estimates before selection; identical canonical partition and base/final order |
| Same fitness-band capsule with valid but substituted partition membership | Replay difference at `$.routing.evaluation.estimator_partition`; stored partition is never replaced before comparison |

Pin group labels/config digest and altered-membership replay failure. Headroom
uses membership even when raw fitness suggests different grouping.

### 8.3 Quality, cost and wrapper

Example floors F>=6000/C>=5000 are test inputs, not defaults.

| Vector | Expected result |
| --- | --- |
| F=5999/6000/6001, C=4999/5000/5001 | Inclusive independent thresholds |
| Unknown F/C, floors zero | Fails; known zero may pass |
| Equal F/C, reversed config order | Lexical id breaks tie |
| Cheap a F=5900/USD=1; b F=7000/USD=10 | b wins; a stays in tail |
| All fail floor | `quality_floor_unmet`, no order |
| All passing tuples incomplete | `cost_unknown`, no order |
| Unknown USD versus explicit zero, other dimensions known | Known zero wins; unknown retained |
| Complete local/metered without class priority | `cost_incomparable` |
| Same competitors with declared class priority | Declared order wins |
| Different subscription units | `cost_incomparable` |
| Cold ensurable local, positive start estimate | Admitted; tuple reflects start cost |
| Missing/extra estimates, bad cost or version mismatch | Refusal, not abstention |
| Mandatory flag false, fallback after floor/cost/reserve abstention | Bound-floor fallback rejection |
| Shadow route | Trial recorded; effective baseline unchanged |
| Empty admitted set | No-candidates; no estimator/assessor call |

Slot vector: base `[a, local-x, b, c]`, groups `{a,b}`, `{local-x}`, `{c}`,
headroom prefers b. Final `[b, local-x, a, c]` has base positions `[2,1,0,3]`.
If a passes and b fails, refuse `headroom_quality_floor_conflict`. If passing
a has complete cost and passing b unknown cost, refuse
`headroom_cost_knownness_conflict`. Pin both guards for same-pair, declared and
fitness groups; check only movable members.

Base abstention with no partition passes through under new selector; old
selector keeps its pre-B refusal. Pin metered/local/ungrouped fixed slots,
unknown singletons and out-of-band quality. Keep existing stale/partial/expired/
invalid/over/reserve/inflight vectors and whole-set all-reserved semantics.
Every successful result meets total-permutation `ValidateAgainst`.

Explanation vectors below use exact bound cost rows with valid source
provenance; all named quantities are integers. In every case, `cost.estimate`
equals the whole `CandidateCost` row, including id, source and omissions.

| Explanation vector | Expected structured cost |
| --- | --- |
| Metered row: `api_usd_micros:0`, `latency_ms:12` | `complete:true`, `comparison_unit:"usd-micros"`; explicit zero retained |
| Subscription row: `quota_demand_bp:25`, `quota_unit:"requests@1"`, `latency_ms:12` | `complete:true`, `comparison_unit:"requests@1"`; exact versioned unit retained |
| Local row: `engine_occupancy_ms:30`, `start_ms:0`, `latency_ms:12` | `complete:true`, `comparison_unit:"milliseconds"`; all three dimensions retained separately |
| Metered row with only `latency_ms:12` | `complete:false`, comparison unit omitted; absent API cost stays absent |
| Unknown-cost row with only candidate id and source | `complete:false`, comparison unit omitted; no quantity synthesized |
| Evaluation with no costs | Whole `cost` member omitted |

### 8.4 Importer, derivation and protocol

| Vector | Expected result |
| --- | --- |
| Complete synthetic TP=8/FP=2/FN=2 | F1=0.8; derivation 2 utility 0.8 direct or 0.4 partial |
| Same set, direct/partial fingerprints | Fitness 9000/7000 |
| Weights 7500 review.code / 2500 review.spec, spec absent | Coverage 7500 direct / 3750 partial |
| Review benchmark under derivation 1 | Unmapped; no reviewer fitness |
| New benchmark version without mapping | Unmapped under derivation 2 |
| Zero F1 denominator | F1 absent; clean report separate |
| Incomplete case set | No comparable full-set aggregate |
| Native round-trip and repeat Store.Add | Same bytes/digests; already present |
| Relocated/reformatted export | Same identity |
| Changed mapping name/version or digest with stale export references | `evidence_evalrun_mapping_mismatch` |
| Changed mapping with matching updated export `mapping_ref` and `mapping_digest`, otherwise valid inputs | Valid new import; new mapping-bound importer version and import digest; original import preserved, reprint origin dedup retained |
| Duplicate result/metric or conflicting shared-result subject | `evidence_evalrun_result_conflict` |
| Absent/unknown effort, including unknown on both sides | Retained; never matched |
| Explicit none or registry-unrecognised token | Known none matches; unknown token retained/reported |
| Unmapped names resembling registry ids, unmapped runtime | Unresolved; diagnostics survive store add |
| Missing protocol or changed protocol at same benchmark version | Protocol-required or benchmark-conflict refusal |
| Zero value vs absent row; zero sample count | Zero preserved; absence unknown; zero count refused |
| Nonfinite/null/unknown field; negative cost | Existing strict/invalid-value refusal |
| Observation and export times differ | Both retained; no clock substitution |
| Reprint or append-only correction | Count once; correction supersedes by address |
| Evalrun --imported-at; other importer --mapping | `cmr_invalid_arguments` |
| JSON CLI refusal | Error stdout, nonzero exit |

Commit estimator full input/view/estimates/partition and canonical bytes/digests;
routing full bundle/decision id/base-final order/explain; importer export/map/
registry/import-snapshot bytes/digests/report/view or error. Preserve old vectors.
Future implementation acceptance includes supported-architecture reproduction
and required repository checks. Corpus collection and actual held-out results
remain separately reported host work.

## 9. PR split and integration gates

| PR | Scope and ordering |
| --- | --- |
| C2 | Partition types/member, validation, refusals, absent-field digest parity; separate review and lane announcement |
| C-quality-cost | Floor/cost fields, versions, reasons, fallback and wrapper guards; separate review/announcement after C2 |
| Estimator implementation (#13) | Existing seam, partition production and audit replay; consumes C2 |
| Routing implementation (#13) | Base strategies, selector dispatch, explain and guarded wrapper; consumes both contracts |
| Evalrun implementation (#14) | Parser/mapping, protocol/manifest artifacts, derivation 2 and vectors; independent of routing contract work |
| Preparation and shadow comparison | Assemble/audit/archive bundles and compare behavior; shared CLI edits serial |
| W4/W5 integration | Role/facet projection, public fingerprints, cost ownership, policy bindings, retention, launch validation and actual-attempt feedback |

No implementation PR hides either frozen-contract change. Review/announce each
contract before dependent implementation lands. Estimator and evalrun use
separate evidence files; serialize shared CLI/docs edits. #15 module access and
#17 canonical helpers are independent, not blockers. Semantic assessment is
slice C, not a dependency. K4 waits for K2a.

W4 preserves operator/project provenance and floor versions; freezes clocks and
public fingerprints; defines retention and cost ownership. W5 follows the integration readiness gate
and W1–W4/tag prerequisites. Live rollout respects M5b and starts shadow, then
recommend. Select belongs to slice D with two-phase validation and effective
configuration proof. Quota/heartbeat changes require fresh availability;
substantive task/role/model/tool/context-lock/network changes stale the decision.

## Pending operator decisions

Exactly these seven product forks remain for the operator. Until the operator decides, defaults
are reversible and used only in `shadow` or `recommend`. Record profiles,
protocol and versions. Illustrative thresholds are not universal requirements.

| Fork | Pending choice | Default until the operator decides |
| --- | --- | --- |
| F1 quality/coverage floors | Numeric floors and allowed support by class | No mandated floor; explicit per-class profiles. Declare both thresholds for either quality strategy. |
| F2 interchangeability tolerance | Equality, wider bins or declared equivalence | Fitness/coverage widths 1 bp by default; widening binds a reviewed config. |
| F3 cross-billing preference | Class priority, calibrated utility or abstention | Operator-declared class priority; mixed classes otherwise incomparable. |
| F4 note influence | Preserve, cap or exclude W2 priors | Keep W2 view; notes add no empirical coverage. |
| F5 grading | Planted matching, adjudication or pinned judge | Planted plus adjudicated ground truth; human resolution of ambiguous matches. |
| F6 objective | Unweighted F1, severity utility or constrained recall | Unweighted F1 first; severity reported separately. |
| F7 corpus | Bounded class or broad suite | One bounded class: Go code review on our repositories. |
