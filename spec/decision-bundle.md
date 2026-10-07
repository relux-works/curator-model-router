# Frozen decision bundle (W1)

A bundle is a JSON object with these exact, case-sensitive members:

```json
{
  "schema_version": "v1",
  "envelope": {},
  "snapshot": {},
  "evaluation": {},
  "policy": {"schema_version": "v1", "mode": "select"},
  "versions": {
    "assessor_version": "unknown",
    "estimator_version": "unknown",
    "selector_version": "router-core-v1",
    "applicability_scope": "workload"
  }
}
```

The empty objects above stand for the complete W0 `TaskEnvelope`,
`CandidateSnapshot`, and `EvaluationSnapshot` documents. See
`testdata/headroom/01_expiring_first/input.json` for an executable example.
All required collections are explicit arrays; unknown optional values are
omitted, and no `null` is permitted. Contract enums, digests, freshness, and
collection ordering follow `contract-appendix.md`. Policy defaults are
materialized by `LoadPolicy` before hashing; other documents have no implicit
defaults. Unknown or differently cased keys, duplicate keys, and trailing JSON
are refused. Libraries consume bytes or immutable values and perform no I/O.

`versions.expires_at` is optional, in supported Unix seconds, strictly after
`snapshot.as_of`. Preparation time is exactly `snapshot.as_of`. The role-context
digest hashes `{schema_version, role, context}` from the envelope; the complete
envelope digest separately binds all task members. Assessor and estimator
versions describe the stored evaluation, using `unknown` when absent. Selection
supports `router-core-v1` (config-order and the headroom wrapper, the
historical behaviour) and `router-quality-v1` (adds `quality-first`,
`cost-with-quality-floor` and fitness-band partitions, `spec/slice-b.md` §4–§5);
any other selector version is refused with `routing_selector_version_mismatch`.

Optional `versions.baseline_fallback_reasons` lists the named abstention reasons
for which the caller explicitly authorizes baseline fallback. Omit it for no
fallback. `versions.mandatory_evidence_floor: true` prevents fallback. These
adapter options are stored here because the frozen routing policy has no
fallback fields. Fallback requires a baseline identical to an admitted variant
in the bound snapshot. It records a new content id, selected outcome, original
abstention reason, and `baseline_after_abstain` origin, and replays from this
bundle. It cannot bypass an evidence floor or a no-candidates outcome.

For `router-quality-v1`, BuildDecision records the strategy result before
fallback; authorized fallback never changes its `abstain` outcome or `router`
origin. Route composes strategy -> BuildDecision -> optional BaselineFallback.
Replay follows the stored origin: `router` replays without fallback;
`baseline_after_abstain` replays the separate fallback using this bundle's
snapshot, recorded selected variant as the fallback baseline, authorized
reasons and mandatory-floor inputs. The quality policy itself also imposes a
floor, even when the adapter flag is false.
Pre-fallback and finalized decisions each have their own content identity.
`router-core-v1` retains its historical replay behavior.

Commands:

```text
cmr route --input FILE [--json]
cmr explain --decision FILE --input FILE [--json]
cmr replay --decision FILE --input FILE [--json]
cmr headroom explain --snapshot FILE [--policy FILE] [--role R] [--json]
```

`route --json` emits `{decision?, effective_candidate?}`. Extract `decision` into
the file supplied to `--decision`. `off` omits the decision and preserves the
first `snapshot.config_order` candidate without evaluating a strategy. It returns
before snapshot validation because R7 requires off to leave the caller's choice
untouched, even when router inputs would be refused in another mode. `shadow`
records the router choice but keeps the baseline effective. `recommend` supplies the
recommendation while preserving the effective baseline for the caller to
choose explicitly. `select` uses the recorded selection; abstention and no
eligible candidates leave the effective candidate absent. Outcomes are distinct
`selected`, `abstain`, and `no_eligible_candidates`; no eligible candidates also
records its named reason. Alternatives exclude the selection and carry current
`position` and the wrapper result's `base_position` when present; final positions
also appear in explain.

`explain` first verifies the content id and replay equality with the supplied
bundle. The JSON includes each candidate's base/final positions, group, billing
class, preserved windows and freshness, applicability, knownness, integer
remaining/time/slack quantities, H/S/band/over/reserve, in-flight counts, reason
codes, and optional display-only credits. The human view renders only those
structured values. All-reserved abstention has no final positions. Decision
explanations also carry outcome, selection origin, and selected id. Explicit
baseline fallback retains the wrapper abstention reason and displays baseline
final positions and its recorded fallback origin.

`replay` returns `{equal, differences}`; each difference names an exact JSON
member (including array index) and its stored/recomputed values. Missing members
are omitted. Equality exits 0, mismatch exits 1, and typed refusals exit 2.
Library replay accepts a nil stored decision for `off`. Errors with `--json`
emit `{error:{code,message}}` on stdout; plain errors use stderr. File failures
use `cmr_input_failed`, flag errors `cmr_invalid_arguments`, and output failures
`cmr_output_failed`, without embedding file paths.

`headroom explain` defaults to enabled headroom with the default policy,
`recommend` mode, and role `unknown`. A supplied policy controls whether headroom
is enabled; `--role reviewer` applies the protected-role reserve behavior. It
reads only the supplied snapshot and optional policy. It runs no probe and
consults no clock, account, model, or live evidence.

Under `router-core-v1` the two slice-B base strategies are refused with
`routing_strategy_not_implemented`; under `router-quality-v1` they run per
`spec/slice-b.md` §4. Fitness-band without a versioned estimator partition is
refused with `headroom_partition_required`. The default wrapper preserves all non-subscription and ungrouped
slots. Same-pair grouping additionally requires identical execution members
apart from candidate/binding/usage identity. A local or metered member prevents
all-reserved abstention because the rule applies to the whole admitted set.

`snapshot.config_order` is required and preserves the caller's declared candidate
order while `snapshot.candidates` stays sorted by id. Both are hashed. The
headroom wrapper fills `base_position` for every ranked candidate; decision
alternatives retain it. Fitness-band groups come from the optional
`evaluation.estimator_partition` (C2); without it fitness-band is refused.

A model scope is unmapped only if no scope-map entry exists for that runtime and
scope: it is ignored with `quota_scope_unmapped`. An existing entry excluding the
candidate's model is simply inapplicable, without that reason; explain records
`applicable: false`. Pace reasons (`quota_expiring`, `quota_on_pace`,
`quota_conserve`) require defined slack S; incomplete facts carry their
incompleteness reasons and any known over condition, without a pace label.
Same-pair labels use `same-pair:` plus 12 lowercase SHA-256 hex characters of a
versioned canonical JSON execution profile with identity members cleared.

Golden updates are explicit:

```text
go test ./pkg/routing -args -update
```

Normal runs compare `testdata/headroom/*/input.json` and `expected.json` without
writing. Expected files contain order, per-candidate reasons, and full explain
JSON, or a stable refusal code. Semantic assertions independently check the
important quantities and ordering before a golden can be updated.
