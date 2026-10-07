# DIGEST contract (W0)

This appendix is normative for the frozen W0 contracts. The task-board boundary,
member-wise spawn validation, and delivery protocol portion belongs to W4.

1. Canonical JSON is UTF-8 without a BOM, insignificant whitespace, or a trailing
   newline. The top level MUST be an object with a non-empty string
   `schema_version`. Every nested `null` MUST be refused; omit optional unknown
   fields. Required unknown string/enum values use the literal `"unknown"`.
   Contract `Validate` refuses empty required strings with
   `contract_empty_field`, naming the JSON path and directing the caller to
   `"unknown"`. Required collections MUST be non-nil; `contract_missing_field`
   names the JSON path. Known-empty is `[]`. These checks recurse through all
   nested contract members, including map keys and values in `Facets` and
   `Features`: empty strings are `contract_empty_field`, and `"unknown"` is
   permitted for unknown map strings. Closed enums still require declared values;
   digest fields follow the shape and unknown exceptions in rule 6. Numeric zero
   is a value, never an unknown marker. Unknown `used_bp`, fitness,
   coverage, and optional timestamps are represented by omitted pointer fields.
   Record-root exception: `canonical.MarshalRecord` and `RecordDigest` require
   an object (`{}` is valid), but neither require nor insert `schema_version`.
   Every member, including `id` and any supplied `schema_version`, is retained;
   evidence removes only the root `id` before hashing. All other rules apply.
   Non-object record roots are `canonical_invalid_json`; null remains
   `canonical_null`. Document helpers retain their schema requirement.
2. Object keys MUST be sorted by unsigned UTF-16 code units, recursively, as in
   [RFC 8785](https://www.rfc-editor.org/rfc/rfc8785). Strings MUST use JCS escaping:
   quote/backslash escaped, the five short control escapes, other U+0000–U+001F
   as lowercase `\u00xx`, and all remaining Unicode verbatim. Do not escape slash,
   HTML characters, U+2028, or U+2029. Do not normalize Unicode. Invalid UTF-8,
   lone surrogates, duplicate object keys, malformed JSON, and trailing data MUST
   be refused. Nesting MUST NOT exceed 1000 simultaneously open object/array
   containers, counting the root object; excess depth is `canonical_invalid_json`.
   Record depth counts from its own root: depth 1000 is accepted and 1001
   refused. This extends the former evidence envelope limit of 999 without
   changing any previously valid record address.
3. Integer tokens represent signed or unsigned 64-bit integers and MUST serialize
   exactly, including above 2^53. Other tokens are IEEE-754 binary64: ECMAScript
   shortest round-trip digits, ties to even, decimal notation for absolute values
   in [10^-6, 10^21), scientific notation otherwise, lowercase `e`, explicit `+`
   for positive exponents, and no exponent leading zeros. Negative zero becomes
   `0`. NaN and either infinity MUST be refused, including overflow on parsing.
   An integral token beyond both 64-bit domains is interpreted as binary64.
   This is an explicit extension to JCS for exact contract integers.
4. Arrays preserve their declared semantic order. Collections declared as keyed
   MUST already be strictly ordered without duplicate keys; hashing MUST NOT
   silently repair them. The operator-facing `LoadPolicy` normalizes only the
   declared policy sets before validation, as rule 5 specifies. `SortByKey` is an explicit caller normalization helper;
   `CheckOrdered` refuses disorder and duplicates, even non-adjacent duplicates.
   Declared collection keys compare lexically by UTF-8 bytes: usage facts and
   in-flight counts by key, windows by id, candidates by id, scope entries by
   (runtime, scope), and scope model ids by id. Evaluation assessments are by id,
   estimates by candidate id, evaluator versions by name, and record addresses
   lexically. Policy masks, protected roles, window-id lists, and reason sets are
   lexical. Alternatives are by increasing current `position`. Task criteria/context/
   tools and declared group/pair lists retain explicit policy/task order.
   `CandidateSnapshot.config_order` is REQUIRED and non-nil (nil is
   `contract_missing_field`); `[]` is valid only when `candidates` is empty.
   It is the operator/caller configuration order, a semantic-order array that
   MUST NOT be sorted or normalized, and the only source of base order for
   `config-order`. It MUST be an exact permutation of candidate ids: every id
   once, with no extras or duplicates. An extra, missing, or duplicate id is
   `contract_config_order_mismatch`, naming the offending id. Extras and
   duplicates are checked in configuration order, then missing ids in candidate
   id order. `candidates` remains keyed and sorted by id; both arrays are hashed.
   `RankedCandidate.base_position` is an optional integer: the candidate's
   position in the base strategy's order before any wrapper permuted it, absent
   when no wrapper ran. If present on any member of a `SelectionResult.order`
   or `RoutingDecision.alternatives` list, it MUST be present on all members of
   that list, non-negative and unique. In a `SelectionResult.order`, which
   ranks every eligible candidate, they MUST also form a permutation of 0..n-1,
   where n is the list's length; `RoutingDecision.alternatives` may omit the
   selected candidate, so gaps are allowed there. Partial presence, negative
   values, duplicates, or (in a selection order) out-of-range values are
   `contract_base_position_invalid`. Zero is serialized; absent is omitted.
   Base positions preserve semantic order and need not increase with current
   `position`; present values are included in decision hashes.
5. Load policy defaults before hashing. Programmatic policies MUST start from
   `DefaultPolicy` and pass `Validate`; `LoadPolicy` materializes defaults while
   preserving explicit zero, false, and empty values. Every key MUST exactly
   match its declared JSON tag, case-sensitively, including nested group pairs;
   unknown or differently cased keys are `contract_unknown_field`.
   Strict decoding of snapshots/envelopes at the task-board boundary is W4's.
   `LoadPolicy` sorts and de-duplicates `free_field_mask`, `protected_roles`,
   and window-id lists. Programmatic `Validate` continues refusing unordered
   or duplicate sets. Other arrays retain their declared semantic order.
   `strategy` names ONLY the base: `config-order`, `quality-first`, or
   `cost-with-quality-floor`. `headroom.enabled` enables the headroom wrapper.
   `strategy: headroom-aware` MUST be refused as `routing_unknown_enum`.
   Defaults are schema version supplied by the caller, strategy `config-order`,
   mode `off`, empty free-field mask; headroom `enabled: false`, equivalence
   `same-pair-any-home`, groups `[]`, protected roles `[orchestrator, reviewer]`,
   reserve 2000 bp, band width 1000 bp, expiring band 3, conserve band -2,
   windows `all`, on-all-reserved `rank`.
   Non-empty groups with another equivalence are `headroom_groups_unused`;
   pair intersections are checked ONLY for `declared-groups`. Omitted runtime
   is a wildcard. Duplicate/intersecting pairs within one group are
   `headroom_group_duplicate_pair`; overlap BETWEEN groups is
   `headroom_groups_overlap`. Group and pair order is preserved.
6. A digest is `sha256:` followed by 64 lowercase hexadecimal digits of SHA-256
   over canonical bytes. `engine_profile.weight_digest` and
   `context_profile.lock_sha256` MUST be either that shape or literal `"unknown"`.
   `usage_facts[].record_digest` is optional in the wire shape: it MUST be omitted
   for state `absent` (a present value is `contract_field_forbidden`), and MUST
   be present and strictly shaped for every other state (`contract_missing_field`
   if omitted). It MUST NOT use `"unknown"` or a fabricated placeholder digest.
   `decision_id` and every other present decision/evaluation/snapshot/policy
   digest MUST retain the strict shape, with no `"unknown"` exception.
   Malformed digests are `contract_invalid_digest`.
   Use contract `Digest` methods to enforce presence and collection order.
   `RoutingDecision.ContentID` hashes the decision with `decision_id` omitted;
   `WithContentID` returns a copy carrying that identity. `ContentID` ignores an
   old id and can run before assignment; `VerifyContentID` MUST compare the
   stored id with the recomputed id and refuse missing, stale or substituted
   ids with `contract_decision_id_mismatch`.
7. `CandidateSnapshot.Validate` checks TTL-independent window facts.
   `fresh`/`stale` MUST have valid ranges, present `used_bp` and `observed_at`,
   observation no later than `as_of`, and absent reset or reset after `as_of`.
   `expired` MUST have valid ranges and observation plus reset at or before
   `as_of`. Contradictions are `contract_freshness_mismatch`. TTL is absent from
   the projection, so validation does not distinguish fresh from stale by age.
   `invalid` remains a permissible label for unknown or malformed facts.
   State `absent` MUST carry no windows: its required window list is `[]`;
   any window yields `contract_field_forbidden`. States `not_supported` and
   `unavailable` MAY retain windows for `explain`, subject to the label checks
   above, but NO window of either state is ever known, whatever its label
   (headroom.md §4). W1 MUST enforce this when deriving ordering quantities.
8. `selected` MUST carry `selected_candidate`; every other outcome MUST omit
   it. `baseline_after_abstain` MUST have outcome `selected`. Present expiry
   MUST be later than preparation; both use the supported timestamp range.
   Violations are `routing_invalid_decision`. Outcome tokens live in
   `OutcomeKind`; only spec-named reasons live in `ReasonCode` (including the
   explicitly named `no_eligible_candidates` and the baseline
   `config_order_preserved`). `bound_on_this_machine` is a
   catalog field, not a reason.
9. `FitnessEstimator` MUST be pure and deterministic over immutable in-memory
   evidence. It MUST refuse a reference whose `EvidenceSnapshotRef.Digest`
   differs from the evidence it holds with a typed `routing.Error`, code
   `estimator_snapshot_mismatch` (constructor
   `NewEstimatorSnapshotMismatchError`). No estimator implementation is part
   of W0. Fitness and coverage are 0..10000 inclusive; absent differs from zero.
10. Refusals are typed, with stable canonical codes: `canonical_null`,
    `canonical_nonfinite`, `canonical_missing_schema_version`,
    `canonical_unordered`, `canonical_duplicate_key`, `canonical_invalid_json`.
    Routing codes include the `contract_*`, `estimator_snapshot_mismatch`, and
    group codes specified above (including `contract_config_order_mismatch` and
    `contract_base_position_invalid`); `routing_unknown_enum`, `routing_invalid_policy`,
    `routing_invalid_as_of`, `routing_invalid_count`, `routing_missing_usage_key`,
    `routing_invalid_evidence_ref`, `routing_invalid_estimate`,
    `routing_invalid_selection`, `routing_invalid_decision`,
    `routing_invalid_percent`, `headroom_invalid_band_width`,
    `headroom_invalid_reserve`, and `headroom_invalid_bands` retain their meanings.
11. CLI refusals with `--json` MUST put the JSON error object on stdout and
    retain a nonzero exit code; plain refusals use stderr. Errors while writing
    output attempt the same channel rule and retain the refusal exit code.
    CLI codes are `cmr_unknown_subcommand`, `cmr_invalid_arguments`, and
    `cmr_output_failed`.
12. C2 adds optional `EvaluationSnapshot.estimator_partition`. Its exact shape
    is `{schema_version, estimator_version, groups:[{id, candidate_ids}]}`,
    with schema `estimator-partition-v1`. An absent partition MUST be omitted,
    never serialized as null or synthesized as an empty partition. All former
    evaluation bytes and digests remain unchanged when it is absent. Presence
    is included in the evaluation digest and therefore the bound decision id.
    Strict loaders enforce exact JSON tags and refuse unknown members and null.
    Groups are keyed by nonempty opaque id, ordered lexically by UTF-8 bytes;
    candidate ids within each group are also nonempty, unique and lexical.
    Hashing and validation MUST NOT reorder or repair either list. Required
    groups/member lists are non-nil (`contract_missing_field`); empty strings
    are `contract_empty_field`. Unsorted or duplicate keys retain
    `canonical_unordered` and `canonical_duplicate_key`.
    `EstimatorPartition.Validate` checks structure and supported schema.
    `ValidateAgainst(candidates, estimates)` additionally requires disjoint,
    exhaustive admitted membership and exactly one estimate per admitted id.
    Singleton groups are valid; `groups:[]` is valid only for empty candidates
    and estimates. Bad schema, empty groups, overlap, extra/missing members, or
    a non-bijective estimate set are `routing_invalid_estimator_partition` in
    C2. Under `router-quality-v1`, C-quality-cost uses the dedicated
    `routing_estimate_set_mismatch` at the selector evaluation boundary before
    checking partition membership, including config-order without a floor.
    Historical `router-core-v1` C2 refusals
    retain `routing_invalid_estimator_partition`.
    Partition estimator version MUST be supported (`suitability-bp-v1` in C2)
    and equal every estimate version. Evaluation validation also requires a
    matching `evaluator_versions` entry named `fitness-estimator`; bundle
    loading, routing, explanation and decision construction additionally check
    `versions.estimator_version`. Missing, unknown, unsupported or inconsistent
    bindings are `routing_estimator_version_mismatch`, except that empty
    required strings retain `contract_empty_field`.
    `fitness-band` headroom consumes only these opaque groups and member lists;
    it never rebuilds groups from fitness, coverage or quota `band_width_bp`.
    Only subscription members permute over their existing base slots; metered,
    local, singleton and ungrouped slots stay fixed. Without the partition,
    retain `headroom_partition_required` before calling the base strategy under
    `router-core-v1`. The new selector calls the base first, as rule 13 specifies.
    Estimation replay (owned by the subsequent evidence implementation) MUST
    regenerate and compare a stored partition before selection; a valid
    substituted membership is a replay difference at
    `$.routing.evaluation.estimator_partition`. An absent partition stays absent.
    C2 selection replay uses the stored evaluation and does not re-estimate.
    Producer bucket/label and estimation-replay expectations are frozen in
    `pkg/routing/testdata/c2/` as contract fixtures; they do not implement the
    estimator, quality selector or evidence-owned estimation replay.


13. C-quality-cost adds optional `RoutingPolicy.quality`, `RoutingPolicy.cost`
    and `EvaluationSnapshot.costs`. Defaults leave all three absent; all prior
    canonical bytes/digests, including floorless historical strategy-name policy
    fixtures, remain identical. Absent members are omitted, never null. Present
    members follow exact JSON tags, strict integer decoding, ordering and digest
    rules. `ValidateForVersion` enforces selector-specific policy requirements;
    `Validate`/`Digest` also accept historical floorless policies. Historical
    `Strategy` remains the core selector; `StrategyForVersion` dispatches new work.

    `quality` is `{rubric_id, rubric_version, estimator_version, scale,
    minimum_fitness_bp, minimum_coverage_bp}`. Both integer threshold keys MUST
    be present in strict loaders, even for zero; neither threshold has a default.
    Thresholds are independently inclusive in 0..10000. Scale MUST be
    `role-utility-bp-v1`; supported estimator is `suitability-bp-v1`. Known rubric
    id/version MUST equal the frozen evaluation rubric. All estimates,
    `fitness-estimator` evaluation provenance and bundle estimator version MUST
    match the bound estimator. Every assessment's canonical requirements MUST
    equal the rubric requirements projection. Unknown fitness/coverage fails
    even a zero floor. Invalid estimates or bindings refuse, never abstain.

    `cost` is `{version, model_version, billing_order}`. Supported comparator is
    `billing-lex-v1`; known frozen model version MUST equal `costs.model_version`.
    `billing_order` is REQUIRED, non-nil and semantic, never sorted or deduplicated
    by loaders. `[]` supplies no cross-class preference; nonempty MUST be an exact
    permutation of `local`, `subscription`, `metered` in operator-declared order.
    No cross-class preference or numeric floor is invented while F1/F3 are pending.

    `costs` is `{schema_version:"routing-costs-v1", model_version, candidates}`.
    Rows are REQUIRED, ordered by candidate id, exactly one per admitted candidate.
    Each `CandidateCost` row is `{candidate_id, api_usd_micros?, quota_demand_bp?,
    quota_unit?, latency_ms?, engine_occupancy_ms?, start_ms?, source}`. Quantities
    are integers in 0..10^15, including zero. Source MUST carry public known
    name/version and a strict SHA-256 digest. `quota_unit` is present exactly when
    quota demand is present and MUST be a nonempty known domain. Unknown forecasts
    retain the id/source row with omitted quantities. Reject class-inapplicable
    quantities; perform no currency, quota or resource conversions.

    `router-quality-v1` implements both new bases. Both require explicit quality;
    cost-with-floor also requires policy and snapshot cost. Quality-first forbids
    cost policy; config-order forbids quality and cost policy but may retain unused
    evaluation forecasts. These restrictions also apply to the public versionless
    `Strategy` factory and `HeadroomStrategy` wrapper, regardless of whether
    headroom is enabled. Floorless historical strategy-name refusals are unchanged.
    Empty admission returns `no_eligible_candidates` before
    quality evaluation/partition checks. Otherwise estimates cover admission
    exactly. Let P be candidates passing both floors. Empty P abstains with
    `quality_floor_unmet` and no order. Quality-first sorts P by descending fitness,
    descending coverage, lexical id, then appends failing/unknown candidates by id.

    Cost-with-floor first compares complete tuples among P: metered
    `(api_usd_micros, latency_ms)`, subscription `(quota_demand_bp, latency_ms)`
    under an equal quota unit, local `(engine_occupancy_ms, start_ms, latency_ms)`.
    All tuple dimensions are required. All incomplete costs abstain `cost_unknown`;
    mixed complete classes without declared priority or different complete
    subscription units abstain `cost_incomparable`. Complete candidates sort by
    class position, ascending tuple, descending fitness/coverage, lexical id.
    Passing incomplete-cost candidates follow by descending fitness/coverage/id;
    the failing/unknown tail follows by id. Every success is a total permutation
    with consecutive positions; a cheaper failing candidate cannot win.

    New reasons are `quality_floor_applied`, `fitness_unknown`,
    `quality_below_floor`, `coverage_below_floor`, `quality_floor_unmet`,
    `quality_first_ordered`, `cost_with_quality_floor_ordered`, `cost_unknown`,
    `cost_incomparable`. New routing refusals are
    `routing_quality_policy_required`, `routing_quality_version_mismatch`,
    `routing_estimate_set_mismatch`, `routing_cost_policy_required`,
    `routing_invalid_cost`, `routing_cost_version_mismatch`,
    `headroom_quality_floor_conflict`, `headroom_cost_knownness_conflict`,
    `routing_selection_mismatch`.
    For all nonempty evaluations under `router-quality-v1`, check the admitted
    estimate bijection before partition membership for every base, including
    config-order without a quality policy or partition. Present cost rows must cover
    the admitted set and be valid even when no partition or floor is present.
    One shared input validator checks policy, structural evaluation, estimate
    bijection, present partition, floor/version bindings and present/required
    cost rows at every quality evaluation boundary. A missing, extra or substituted
    estimate
    refuses with `routing_estimate_set_mismatch`; `router-core-v1` retains the
    historical `routing_invalid_estimator_partition` refusal.
    Direct `StrategyForVersion` selection enforces the same boundary with headroom
    enabled or disabled. A public `HeadroomStrategy` wrapping a versioned base
    retains that selector's validation order, including through nested wrappers.
    The versionless `Strategy` factory continues to dispatch `router-core-v1`.
    Existing structural/canonical, invalid-estimate, invalid-policy and C2 codes
    retain their meanings. Floor decisions always carry `quality_floor_applied`;
    `BaselineFallback` rejects that marker or the existing mandatory-floor flag.
    Floor, cost and reserve abstentions cannot unlock fallback, even with the
    bundle flag omitted/false and all reason codes explicitly allowed.

    New-version headroom calls the base first and passes abstention through before
    requiring a partition. Resolve groups using the configured source only. Each
    movable subscription subgroup MUST be homogeneous in floor pass/fail; passing
    cost-with-floor members also MUST share complete/incomplete cost status. Refuse
    either conflict; never split groups, drop members or re-bin around floors.
    One shared composition validator applies these checks in headroom evaluation.
    Under `router-quality-v1`, BuildDecision re-runs StrategyForVersion and Select
    on the same frozen task, snapshot, evaluation and policy, including the final
    headroom wrapper when enabled. It refuses a structurally valid supplied
    SelectionResult that differs from that recomputation with the typed code
    `routing_selection_mismatch`. Equality covers the entire result: ordered ids,
    current and optional base positions, ordered reason codes, and abstention
    presence/reason. Recomputed strategy/input/composition refusals take precedence
    over this mismatch. The rule also applies with headroom disabled and to
    config-order under the new selector. It never calls Route or BuildDecision
    recursively. BuildDecision records the strategy decision BEFORE fallback:
    an abstention remains `abstain` with origin `router`, regardless of fallback
    authorization in DecisionOptions. Route runs strategy -> BuildDecision ->
    optional BaselineFallback. That separate transformation records a selected
    decision with origin `baseline_after_abstain`, the original abstention reasons
    and a new content id; it cannot bypass either floor guard.
    Replay under `router-quality-v1` follows the recorded stage. A stored `router`
    decision replays strategy and builder without fallback, even if the bundle
    authorizes it. A stored `baseline_after_abstain` decision also applies fallback
    with the same bound snapshot, recorded selected variant as its baseline input,
    authorized reasons and floor inputs. The public fallback seam permits any
    identical admitted variant; replay never substitutes the first configuration
    candidate for a different explicitly recorded baseline.
    Accepted builder decisions replay equally; finalized decisions are compared
    to finalized Route results. Mode off still skips decision construction;
    `router-core-v1` retains historical BuildDecision behavior byte-for-byte.
    Base abstention and empty admission retain precedence. Standalone base
    comparators and LoadBundle's input validation do not apply wrapper semantics.
    The public versionless `HeadroomStrategy` also applies these guards when
    supplied a bound quality policy. It validates the bound evaluation
    before invoking any injected base, including an abstaining base. Empty
    admission still skips evaluation validation, and base abstention still
    precedes the requirement to supply an absent fitness-band partition.
    Local/metered/ungrouped slots stay fixed.
    Subscription slots keep base positions
    and use the historical headroom comparator. All-reserved still quantifies over
    the whole admitted set. Within declared equivalence, headroom may override cost
    tuples; the composed order does not promise a global cost minimum.

    Route, BuildDecision, Explain, ExplainDecision and Replay use the stored
    selector version, including internal explanation calls. `router-core-v1`
    keeps historical behavior/refusals/explanations; unsupported versions retain
    `routing_selector_version_mismatch`. Quality/cost policy requires the new
    selector. DecisionBundle's shape is unchanged; decisions bind every new member
    through policy/evaluation digests and content identity. Selection replay uses
    stored forecasts and never re-estimates costs.

    New structured explain adds optional exact `estimate`, `quality` as
    `{passes_floor, reason_codes}`, and `cost` as `{estimate, complete,
    comparison_unit?}`. Cost is emitted exactly when a bound candidate row exists,
    including wholly unknown or unused baseline forecasts. Cost estimate is the
    entire row with omissions, explicit zero and source preserved. Complete means
    every class tuple member/unit is present; it does not assert comparability.
    Emit comparison unit exactly for complete costs: `usd-micros` for metered,
    the exact quota unit for subscription, `milliseconds` for local. Human explain
    renders these structured values. Abstention has no final positions; existing
    required base-position display fields use configuration positions when no base
    order exists. No measurement provenance is inferred from utility scores.

    CQ fixtures in `pkg/routing/testdata/cq/` contain full canonical bundles,
    policy/evaluation/decision/explanation bytes and SHA-256 digests. Decision
    content ids omit the id; decision document digests include it. Expected bytes
    were independently authored with literal result shapes and Python JSON/SHA-256;
    tests only compare. The test floors 6000/5000 are illustrative inputs.

| CQ vector | Policy digest | Evaluation digest | Decision content id |
| --- | --- | --- | --- |
| `quality-first` | `sha256:76ed477c2bc62266a3e122bd6d44ea5b26244f25bdd1d873f9b913d65f83caae` | `sha256:7be6ed0dde7f6897dca661bafd06ca2722b9e1cd1e01641c3492ff50d33f085d` | `sha256:42ed8c247fffce900cf81a60f3eeb6a1231773a3797ea1cebbc9aca35fd2e356` |
| `cost-with-quality-floor` | `sha256:90a8f707e74bed6c809be1131626dfaf09d32a08575d09c1b28cb27a44a50c0b` | `sha256:7be6ed0dde7f6897dca661bafd06ca2722b9e1cd1e01641c3492ff50d33f085d` | `sha256:d43f572a474361629765a1aedb9580ce30bde935fa8babbf62bbd969cf3509a2` |
| `headroom-slots` | `sha256:8214769aa15a538fcc03559a4351377384df6ef9cf1fe28672ca0c466deee968` | `sha256:37c9a729486accfbf068df618647f67953562980de416afe4ecca64a2ac3d522` | `sha256:2f80340fc3755e4cb0c1c283fa5c3392b19b690d5ee1e240abc7a9f72c3e5365` |

Vectors live in `pkg/canonical/testdata/vectors/`: `.input.json`, exact
`.canonical.json` bytes (no newline), `.digest`, or `.error` for a refusal.
`.ordered` declares `items` ordered by id for the collection-validation vectors.
`go test ./pkg/canonical -args -update` is the only golden rewrite mode; normal
runs MUST only compare. Expected bytes were authored independently of the encoder.
The three depth vectors below are generated in `TestDepthVectors` rather than
stored as large JSON files; both array and object nesting boundaries are checked.

| Vector | Digest or refusal |
| --- | --- |
| `array-top` | `canonical_missing_schema_version` |
| `duplicate-collection` | `canonical_duplicate_key` |
| `duplicate-object` | `canonical_duplicate_key` |
| `empty-schema` | `canonical_missing_schema_version` |
| `escapes` | `sha256:897cc57297e5765678b44dcf19cb8f01937bce2513c2c78bfe5e7f5362909d7f` |
| `float-boundaries` | `sha256:1c148f29706f1c4440bef011057bbea24dacc2457e244c0a64146638f28d7e24` |
| `floats` | `sha256:eeeedcfdb5051fafc8ed78707d4ecf2fa740f8e668e18a93c4a6956ec5ebb2f8` |
| `integers` | `sha256:0aa457ac99ba61dd40a007a71658af244866e50f6a8c0508e1390159a2b91fcb` |
| `invalid-utf8` | `canonical_invalid_json` |
| `invalid-json` | `canonical_invalid_json` |
| `keys` | `sha256:a87019fab9530d31e08504c96df4b3131ee722df5cd966654bf40e0df166be6a` |
| `lone-surrogate` | `canonical_invalid_json` |
| `missing-schema` | `canonical_missing_schema_version` |
| `multiple-values` | `canonical_invalid_json` |
| `negative-zero` | `sha256:6878ce440730e009d7baa2d5ee7c2aca368111e3b2d73b83933bbb93feb20f6a` |
| `nested` | `sha256:085e7e7b8e6e3b5ed4b300912bf610ad27bc64904fcf9e4db8419a12fce5e3c1` |
| `nonfinite` | `canonical_nonfinite` |
| `null` | `canonical_null` |
| `ordered` | `sha256:b3ae6b8681bb296c4db379608e92aa715d203d13a38543ee2ae792b9abd4be1b` |
| `surrogate-pair` | `sha256:218fd3c7f39f2de83f4cb32ada4d8266c887aad4da698ee46ed8b0b49096ca1f` |
| `unicode-keys` | `sha256:9a1ef3fd8204f252d62290c95d431ea8072ad41db6510b3b7425496eeabc7d87` |
| `unknown` | `sha256:b46f13e0660b01a272b240bc5e19c9b3b34ed1dc2b535644c0432a8120fa2f5e` |
| `unordered` | `canonical_unordered` |
| `wrong-schema` | `canonical_missing_schema_version` |
| `depth-999` (generated) | `sha256:8a3c9d6d78c6bb6b0f32aaa2b87e94244a6179ae703ec718be052759dd90c8e7` |
| `depth-1000` (generated) | `sha256:d314e865c86cb2ecfffec871138a28151ca9dd7de3a4aadc9792f5d828c2c7ec` |
| `depth-1001` (generated) | `canonical_invalid_json` |

Record vectors live in `pkg/canonical/testdata/records/`, using the same file
suffixes and exact-byte convention. Normal runs only compare; these expected
bytes are independent literals or schema-stripped document vectors, and
digests were calculated independently with SHA-256. `TestRecordDepthVectors`
generates array, object and mixed record depths 999/1000/1001; the pinned
digests below describe `{"nested":` followed by depth-minus-one arrays around
`0`, then `}`. `TestRecordAddressGoldenCompatibility` compares every addressed
evidence fixture record with the former envelope algorithm and checks stored
addresses for valid vectors. Evidence goldens remain byte-identical.

| Record vector | Digest or refusal |
| --- | --- |
| `array-top` | `canonical_invalid_json` |
| `bool-top` | `canonical_invalid_json` |
| `duplicate-id` | `canonical_duplicate_key` |
| `duplicate-object` | `canonical_duplicate_key` |
| `empty` (`{}`) | `sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a` |
| `escaped-duplicate` | `canonical_duplicate_key` |
| `escapes` | `sha256:53e2b20e7a6b7b40902e242324734c8d1558849114fd71c187f05f088b7425cb` |
| `float-boundaries` | `sha256:4a9b4bd6df18c057909bf483a9cf6159d077b8896063d00bfc9775582f6a67cb` |
| `floats` | `sha256:7c892d3452ad85ad65857a43e8dcac93b79475d2334fc3e85bac5c599142c158` |
| `integers` | `sha256:52dadc73f683a16f432c0c3b186408a1118b87cbcf6cf95f8fb786193046c1ae` |
| `invalid-json` | `canonical_invalid_json` |
| `invalid-utf8` | `canonical_invalid_json` |
| `keys` | `sha256:c2985c5ba6f7d2a55e768f92490ca09388e95bc4cccb9fdf11b15f4d42f93e73` |
| `lone-surrogate` | `canonical_invalid_json` |
| `low-surrogate` | `canonical_invalid_json` |
| `members` (`{"a":[2,1],"id":"keep","z":0}`) | `sha256:005479518424ec6600f1a6fa32365f8b8e1a1635eb0eedeba642da97114cd8e6` |
| `multiple-values` | `canonical_invalid_json` |
| `negative-zero` | `sha256:c8f4c040bb70540ca5df8ceefb53c9ebafbc251a1b668ef955d4b01daea0dce7` |
| `nested` | `sha256:f453cd64380a6ab62d977c44a2d6519b8a84698817f833ea99147c7bdfa17b6b` |
| `nonfinite` | `canonical_nonfinite` |
| `null-top` | `canonical_null` |
| `null` | `canonical_null` |
| `number-top` | `canonical_invalid_json` |
| `one` (`{"a":1}`) | `sha256:015abd7f5cc57a2dd94b7590f04ad8084273905ee33ec5cebeae62276a97f862` |
| `ordered` | `sha256:2db54dc4fc276a3a0fa70944d73aac15d00cfbe5951535488ccfb1b9e6d3a92e` |
| `schema-content` (`{"id":"keep","schema_version":false}`) | `sha256:da80743aa2ef13710af662221a3b64caa16317613907a3cbcc655f939df71011` |
| `string-top` | `canonical_invalid_json` |
| `surrogate-pair` | `sha256:f9949e1006d1ca22bc0b60ea94f09779f11a8f0a29bae250fc7ab313d879f5e7` |
| `unicode-keys` | `sha256:62a2501b676fcdf774de8c5b88c032c0bcc863dcc9b589d8cf42f3b1b72b5f91` |
| `unknown` | `sha256:7de97408ae96e55bb65d4faaa2cfc708a8039a6089c2b85e877bc2c1ba22c019` |
| `depth-999` (generated) | `sha256:06ce5cbfd0a20f0b6e452865d766e1edb44add39dfbfc511980dbddee074e729` |
| `depth-1000` (generated) | `sha256:62bc31a10cb369fcb297106db4d0ed0809d71f92d455fa52710e21fcf30e3549` |
| `depth-1001` (generated) | `canonical_invalid_json` |

Routing goldens live in `pkg/routing/testdata/`. The minimal-input default policy
pins both materialized canonical bytes and digest. The rich candidates and
non-default policy also pin canonical bytes; C1 candidates and decision fixtures
pin canonical bytes as well. All fixtures pin digests.
All newly added expected bytes/digests were independently authored from literal
JSON with sorted ASCII keys and SHA-256, without using the production defaults
or encoder. Normal runs only compare.

| Routing fixture | Digest (decision uses content id) |
| --- | --- |
| `envelope` | `sha256:e619e4332988da914296dce3089a38ef796ed56f3ad0a3e9b8c311d658eaf7df` |
| `candidates` | `sha256:453fcfbfbb2d45097c8e64417306ca0c9000d4a7e27f1bb31eab617828ab45b7` |
| `candidates-rich` | `sha256:ad3f75ce683af0cb4a47a62a37f2504ce9502095949e0237740ebf4629c85cec` |
| `evaluation` | `sha256:8425c15c52a90e36df68b2357973e1edef45c68e990bec640b7a4850f9a6c68c` |
| `policy` | `sha256:4901016a8b88cd9979f8f3bfadba6f81a421a11c917d1ab738c28a0f587b53ef` |
| `policy-default` | `sha256:cc2682ca409195e06263f4a12627eda2014bee142f3ec34ac7b5d26dfd8b0117` |
| `policy-nondefault` | `sha256:c7c16d82cd085867dbb82c367b7437ca06356bc356c52c7b28a0ccaa212ed4c4` |
| `decision` | `sha256:1228405101a544c94998bae4d1d099908af4913b75d814c45c4f333e16ecdf31` |

C2 fixtures use the existing routing `-update` flag, after semantic assertions.
Normal runs only compare; all former goldens remain unchanged. The partition,
evaluation and decision below pin canonical bytes and digests (decision uses
content id). Bucket fixtures additionally pin exact producer labels and the
config digests for widths 1 and 250; unknown values have separate singleton ids.

| C2 fixture | Digest |
| --- | --- |
| `c2/partition` | `sha256:6dc91fd64d172816ed8c6822848f3325ac4b27ac1eb389d5ab12a70a602470da` |
| `c2/evaluation` | `sha256:dab4c365b4d7266bed615c115082546cea1be8ad79100701c6a51a37873fab2d` |
| `c2/decision` | `sha256:00735756009cd38045c6bd4c12c463854c4c08fed7002b3d627516076a7934fd` |
| Producer config, fitness/coverage widths 1/1 | `sha256:60f42d9a9500dad6e4c336153c489b40493aec8fb781d759636a6ffe8861a73a` |
| Producer config, fitness/coverage widths 250/1 | `sha256:1fb369fc55aecb2c366e07618df119640deb315a9f60ca329d245a6510dd2b28` |
