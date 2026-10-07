# Recommend: task-aware model selection, plug-and-play (v1.1)

Status: **DECIDED for implementation**, 2026-10-04. The operator's instruction: «прими наиболее подходящие
решения и запустим имплементацию… нужна уже библиотека + её соединения с task-board спавнами… желательно
плаг-н-плей». It resolves F1–F8 for v1 (§7). It builds on the landed contracts (`pkg/routing`, `pkg/evidence`,
`spec/headroom.md`, `spec/slice-b.md`) and does not change them.

## 0. The pains it removes

1. **Hand-written routing rules.** Today rules say which model and effort to use for each role, and they are
   edited by hand when a quota or budget runs out.
2. **Wrong reasoning effort.** Hard-coded rules spend strong reasoning on trivial tasks, or use too little.
   For example, Muse is usable only at `max`, and the gap from `xhigh` is large.
3. **No quality–cost frontier.** Decisions do not use an index-based Pareto curve of quality against cost,
   supplied by a catalog or an explicit operator overlay.
4. **No escalation.** Very hard or delicate tasks need the strongest model. Some research needs a pipeline
   that runs several of the strongest models.

## 1. Unit of choice and the catalog

The unit is a **configuration**: `runtime + model + effort`. Every effort is its own row, so choosing an
effort is choosing a row.

The **catalog** (`catalog/catalog.json`, versioned, with provenance) has one row per configuration. A row
carries:
- `runtime`, `model`, `effort`, `family` (the vendor), `billing` (`subscription | local | metered`);
- `quality`: the indices `{overall, coding, review}`, each 0–100 or absent. Every value has
  `{value, kind: measured | vendor | estimate, source, as_of, stderr?, n?}`. Optional `review`
  stays in catalog-v1 and requires `kind=measured`, `stderr` and positive integer run count `n`;
- `cost`: the relative cost per typical task, `{usd_per_task?, tokens_per_task?, kind, source, as_of}`;
- `latency`: `fast | medium | slow`, or absent;
- `constraints`: for example `{only_effort: "max"}` for Muse, or `not_for: ["windows"]`. Constraints are
  hard filters that the catalog declares.

**Primary quality and fallback (public selector `recommend-v5`).** code.*, tool-use and ops use coding; review.* uses review; all remaining classes use overall. If the primary is absent, use the Bug Hunt review measurement. No coding/overall substitution is allowed. A row with neither source is unknown and never selected.

Every primary-source candidate ranks ahead of fallback-only candidates after admission, hard rules, billing and tier floors, including economy, balanced, burn and fan-out. Quality values are compared only within the same scale. `quality_index` names `coding`, `overall`, `review`, `bughunt_fallback` or `unknown`. Explanations include `quality_from_bughunt_fallback` when applicable and carry the quality value's provenance. Headroom and standing preferences cannot move a fallback ahead of a primary.

Index tiers use `tier_edges`: S ≥62, A ≥56, B ≥45; measured tiers use `review_tier_edges`: S ≥40, A ≥30, B ≥20. Both edge sets are operator-overridable, and tiering always uses the chosen task quality, subtracting one stderr when supplied. Review primary and Bug Hunt fallback use measured edges; index primary uses index edges. These are routing bands, not validated project accuracy thresholds.

For review quality objectives, measured means differing by less than the larger SE tie; exact equality also ties. Anchor each band at the highest remaining mean, then sort by cost/key without chaining bands. Burn prefers known headroom inside each band; matching scoped reviewer preferences break ties within bands. Other task classes retain exact quality ties.

The public embedded catalog has no overall or coding indices. Its measured review values and resource costs come from pinned Bug Hunt runs; cost is a list-equivalent estimate for that workload, not an arbitrary project's bill. Unknown costs remain absent. Vendor list prices and independent authored assumptions are documented in [catalog notes](../catalog/NOTES.md). Bug Hunt is a TypeScript defect-finding/fixing proxy, not measured Go or spec-review accuracy. See [the importer](../tools/review-import/README.md) and NOTICE.

## 1.1 Explicit operator overlays

`LoadOverlay([]byte)` loads strict `overlay-v1` JSON. `MergeCatalog(base, overlay)` returns a detached, key-sorted merged catalog. `Request.CatalogOverlay` applies the overlay when building a decision; `OverlayDigest` binds its canonical semantic digest. No library function discovers a file or a home directory.

Rows are nested objects keyed by exact runtime → model → effort. A patch may contain only quality and cost. Missing fields preserve their base values; explicit zero replaces them. Every quality scalar carries `value`, `kind`, `source`, `as_of` and optional uncertainty; review requires measured kind, stderr and positive n. Each cost scalar has its own value/kind/source/as_of. Token values are nonnegative int64; USD is finite and nonnegative. Unknown rows, empty patches, duplicate keys, unknown or case-variant fields, nulls, trailing documents and malformed values refuse with `invalid_catalog_overlay`. Overlay values never change admission or constraints.

```json
{
  "schema_version": "overlay-v1",
  "rows": {
    "codex": {
      "gpt-6-luna": {
        "low": {
          "quality": {
            "coding": {"value": 91, "kind": "estimate", "source": "FICTIONAL example, not benchmark evidence", "as_of": "2026-10-07"}
          },
          "cost": {
            "usd_per_task": {"value": 0.001, "kind": "estimate", "source": "FICTIONAL example cost", "as_of": "2026-10-07"}
          }
        }
      }
    }
  }
}
```

The example is deliberately invented. Artificial Analysis is an optional source an operator may supply under its own terms; none of its values are embedded or shown here.

CLI: policy `catalog_overlay = "./operator-overlay.json"` or `--catalog-overlay FILE`; the flag takes precedence. Relative paths resolve from the working directory. Missing or invalid explicitly selected overlays refuse. There is no implicit overlay lookup. Library API callers supply bytes or decoded overlays themselves. Effective catalog, complete overlay and canonical overlay digest are frozen into the decision and its content ID; replay uses only frozen inputs and validates the digest, without rereading any file. Scalar cost provenance is retained in `usd_per_task_provenance` and `tokens_per_task_provenance`; absent scalar provenance uses the legacy common cost provenance.

**The frontier.** For each task class, the catalog yields the Pareto frontier of
(quality ↑, cost ↓). A row is dominated when another row is at least as good on both axes and better on one.

## 2. Task profile (input)

```text
TaskProfile
  role         developer | reviewer | researcher | orchestrator | <any playbook role>
  task_class   code.implement | code.fix | code.refactor | code.test | review.code | review.spec |
               docs.write | research | planning | orchestration | tool-use | ops | routine   (taxonomy v1)
  difficulty   trivial | routine | standard | hard | critical          (default standard)
  sensitivity  normal | delicate                                     (default normal)
  language     optional facet (go, swift, …)
  pipeline     single | fanout                                       (default single)
```

Defaults and inference:
- An explicit `--difficulty` always wins.
- Otherwise `difficulty` comes from `task_class`:
  - `routine` and `docs.write` → `routine`;
  - `research`, `planning` and `orchestration` → `hard`;
  - everything else → `standard`.
- `sensitivity: delicate` is set by the caller, for example for security, data-loss paths or a landing on a
  shared main.

## 3. Policy (operator file, sensible defaults built in)

Budget policy `budget_mode = "economy" | "balanced" | "burn"` is independent of the
spawn execution `mode`. `--budget` overrides it for both recommend and spawn. Balanced
is the default and uses the difficulty table below within each quality-source group.
The policy and its override are frozen into the decision and content ID. An absent
`budget_mode` is the legacy wire representation of balanced; recommendation JSON omits
that default to preserve existing golden bytes. Human explanations always print the mode.

| Budget | Objective at every difficulty | Fan-out |
| --- | --- | --- |
| `economy` | Cheapest qualified; standard skips the ≤1.5× A upgrade | Existing distinct-family cap |
| `balanced` | Existing difficulty table below | `fanout_k`, default 3 |
| `burn` | Best quality; equal-quality ties prefer the largest known subscription headroom, then cost/key | Every qualified distinct A+ family up to `burn_fanout_cap`, default 4 |

Budget modes keep admission, locks, constraints, billing order, standing hard rules and
tier floors unchanged, including the existing delicate S-to-A fallback. Economy does
not let headroom move a more expensive row ahead of the cheapest row. Burn keeps
quality ahead of headroom, with anchored review noise bands as above. Non-review
standing preferences retain their existing bias inside each qualified tier and quality-source group. Unknown or stale
usage is neutral and cannot count as spare quota. The cap is configurable up to 100;
zero/omitted `burn_fanout_cap` uses four. Automatic per-runtime mode switching is deferred.
All new decisions use `recommend-v5`. Older records retain their selector version and are refused if replay differs under the public source-selection semantics.

The policy lives at `~/.curator/routing.toml`. A missing file means the built-in defaults. Every value is
versioned and digested into the decision.

| Difficulty | Minimum tier | Objective |
| --- | --- | --- |
| `trivial` | C | cheapest on the frontier |
| `routine` | B | cheapest on the frontier |
| `standard` | B | cheapest that meets the tier, preferring A when A costs ≤ 1.5× the B pick |
| `hard` | A | best quality among A or above; cost breaks ties |
| `critical` | S | best quality, cost ignored |

Rules:
- **Delicate tasks** require tier S and maximise quality. They never fall back below A. If no A-or-above
  candidate is admitted, the router abstains with `no_qualified_candidate`.
- **Fan-out** (`pipeline: fanout`) returns the top K = 3 configurations at tier A or above. The rows must
  come from **distinct families**, ranked by quality, so that one research question goes to several strong
  models.
- **Billing order: `subscription`, then `local`, then `metered`.**
  - Metered is used only when no subscription or local candidate qualifies.
  - When `allow_metered = false` (the default), metered is never used.
- **Headroom.** Among candidates that meet the tier, with the same tier and cost within ±25% (the
  interchangeable set), order by subscription headroom per `spec/headroom.md` §5:
  - prefer capacity that expires soon;
  - keep the reserve (`reserve_bp` 2000) for orchestrators and reviewers;
  - spread runs that are in flight;
  - treat a stale or unknown value as neutral;
  - put an over-quota candidate last.

  Headroom never moves a candidate across tiers.
- **Unknown quality** (tier U) is never selected. It is shown in
  `explain`.
- **Notes do not score.** Operator notes become catalog `constraints` or policy overrides.

### 3.1 Standing rules (operator rulings as hard constraints)

The orchestrators already follow model rulings from the coordinator. Until the operator retires them, the
recommender applies them as **hard constraints**, and every recommendation names each ruling it applied. The
rules live in the policy file as a `rules` list. Each entry has:
- `id` and `source` (the ruling, for example `operator ruling: host reviewer pin (example)`);
- `when`, exact conjunctive matches on `host`, `role`, `task_class`, `runtime`, `model`, `story`,
  plus `difficulty` (a list of trivial/routine/standard/hard/critical) and `sensitivity`
  (a list of normal/delicate). Absent lists match any value; present lists must be
  nonempty, unique and contain only valid values. Task defaults are resolved first;
  scoped-rule explanations record `rule_not_applicable: difficulty` or `sensitivity`
  when the task falls outside the rule's scope;
- one or more actions:
  - `require` / `forbid` a runtime, a model, a family or an effort;
  - `prefer` an order of runtimes or families (a soft bias within qualified tiers, with the
    high-stakes review quality-tie behavior below);
  - `effort` (a pin, or a minimum and maximum);
  - `quota_stop_bp`: a subscription whose tightest applicable known window has headroom at or below this many
    basis points is excluded. Unknown usage never triggers the stop;
  - `cross_provider_review`: a reviewer from a family other than the producer's, when `producer_family` is given;
    same-provider is allowed only as the rule's stated fallback.

Rules are evaluated in file order:
- `forbid` beats `require`;
- a rule that leaves no qualified candidate yields `no_qualified_candidate`, naming the rule.

The host is `--host`, or the policy's `host`, or the machine's short hostname.

The repository ships `docs/standing-rules.toml` as an example operator policy (customize it and copy it into
`~/.curator/routing.toml`):
- **Build-host preference and reviewer pin:** codex first everywhere; only normal trivial/routine/standard
  reviews are pinned to `gpt-6-astra` at `medium`. Hard, critical and delicate reviews
  retain their tier floors and choose the best qualified configuration, preferring
  Codex among candidates in the same anchored best-quality band. The Codex-first
  preference explicitly lists all difficulties; only matching preferences with
  their own difficulty/sensitivity scope get this demanding-review tie behavior.
  Nonmatching rules have no effect on selection or other rules. Preferences never
  move a lower-quality band, or a candidate that has only fallback quality, ahead of a
  candidate with the class's primary quality source.
  Economy retains its cheapest-qualified objective and existing within-tier preferences.
- **Quota stop:** codex weekly window stop when headroom is 25% or less.
- **Cross-provider review:** cross-provider review; same-provider only as the last resort, with the host reviewer pin as the operator
  exception on the build host.
- **Refusal rotation:** on a refusal, rotate the model. The caller passes `--exclude <runtime/model>` and the rule
  records why.
- **Second-host pairs:** the second-host pairs.
- **Per-Story operator exceptions,** for example `<project>/<story>` on sol high, as `story` matches.

The recommender never changes a ruling by inference: replacing one is a card for the operator.

Host limits stay with task-board and the host (disk lines, ceilings, the shared FIFO). `cmr spawn` refuses
nothing on their behalf and passes through task-board's own refusal.

## 4. Candidates (hard filter: admission is never widened)

The admitted set comes from, in order:
1. `--candidates FILE`, a JSON list of `{runtime, model, effort}`;
2. task-board's spawn-preflight for the role. `cmr` calls
   `task-board q 'project_config(view=spawn-preflight, role=R, agent=A)'` for each allowed agent and reads
   the admitted pairs;
3. if neither is available, the catalog rows whose runtime binary is on `PATH`. In that case the decision
   says so.

Every recommendation is a subset of the admitted set. Locked values are respected (R7): if the caller passes
`--agent`, `--model` or `--reasoning-effort`, the router only fills the rest.

## 5. Usage facts (quota)

`cmr usage refresh` runs the providerquota plans from skill-agents-management v0.5.40:
- codex app-server `account/rateLimits/read`;
- claude `-p /usage`;
- muse MSP `usage/read`;
- agy `/usage`.

These are read-only, make no inference call, and run only through the harness binaries. The command runs each
plan at most once per `(runtime, home)` per TTL (10 min), and caches the records under
`$XDG_STATE_HOME/curator/model-router/usage/`. `cmr recommend` reads the cache, refreshes stale entries when
`--refresh` is passed, and projects the records into `routing.UsageFact`, freezing `as_of` once.

The executor lives in `cmd/` (internal), never in `pkg/`; the library stays pure. This is a deliberate v1
placement of Lane 2's executor beside the router, so the router works stand-alone. Task-board's own executor
(W5) can replace it later.

## 6. Output and the task-board connection (plug-and-play)

`cmr recommend --role R --task-class C [--difficulty D] [--budget economy|balanced|burn] [--delicate] [--fanout] [--json]` prints:
- the selected configuration;
- the ranked alternatives, each with its tier, quality, cost, headroom and reason codes;
- the exact task-board spawn flags (`--agent X --model Y --reasoning-effort Z`);
- a content-addressed decision id.

The decision record is written to `$XDG_STATE_HOME/curator/model-router/decisions/`.

`cmr spawn [recommend flags] -- <task-board spawn args>` is the plug-and-play connection:
1. It queries admission with the forwarded board/root selectors, freezes the confirmation policy, and computes the recommendation, injecting only the flags the caller did not set.
2. It appends `--selection-rationale "cmr:<decision-id> <short reason>"` only when the selected provider/role preflight policy accepts or requires it (v2 ordered criteria or v3/v4 `adjustment_confirmation = "required"`). Caller rationales are preserved. For absent/equal criteria, no effective allow-set, `none`, or candidate-file/PATH admission, no rationale is injected. The decision id is always stored and printed by cmr.
3. It execs `task-board spawn` with the result.

Modes, set by `--mode` or the policy:
- `select` (the default for `cmr spawn`, since using it is the opt-in);
- `recommend`, which prints the flags and does not exec;
- `shadow`, which is fail-open: execute the original spawn args unchanged even when
  recommendation, admission, preflight, policy, usage or logging fails. Log the
  would-be decision or error as `cmr:shadow ...` on stderr and return task-board's
  exit status. Only wrapper parse errors before `--` and a missing task-board
  binary stop launch. Resolve mode from the explicit flag first, then a minimal
  independent read of the top-level policy `mode` when full loading fails. A
  readable shadow declaration survives syntax and conversion errors elsewhere;
  an unreadable policy supplies no mode and select/recommend refuse. Explicit
  `--mode shadow` always launches. Shadow preserves the completed child’s exit
  status even if copying stdout or stderr fails; failure to start still refuses.

On `no_qualified_candidate`, select/recommend refuse and never launch anything;
shadow still forwards the original launch.

A fan-out recommendation prints one spawn line per model, and `cmr spawn --fanout` launches each.

A native `task-board spawn --route` flag (W5) can replace the wrapper later without changing `cmr`.

## 7. F1–F8 for v1 (decided; reversible by editing the policy or catalog)

| | Decision |
| --- | --- |
| **F1** floors | Tier floors per difficulty (§3), from the chosen task quality scale. No per-class empirical floors yet. |
| **F2** interchangeable | Same tier, and cost within ±25%. Headroom moves work only inside that set. |
| **F3** billing | `subscription`, then `local`, then `metered`; metered is off by default. |
| **F4** notes | Notes never score; they become constraints or policy overrides. |
| **F5–F7** | Unchanged: the internal review set is planted plus adjudicated, the metric is confirmed majors, the first class is Go review. Its results feed `quality` rows of kind `measured` later. |
| **F8** Muse | Pull-only `usage/read`; no key minting. |

## 8. Acceptance

- Deterministic: the same catalog, policy, candidates and usage snapshot give the same decision. Replay
  reproduces it.
- Golden scenarios:
  1. a trivial task picks a cheap, low-effort row;
  2. a hard task picks tier A or above;
  3. a delicate task picks the strongest row;
  4. Muse is only ever proposed at `max`;
  5. an exhausted subscription moves a standard task to an equivalent row with headroom;
  6. fan-out picks three distinct families;
  7. metered is never chosen when off;
  8. locked flags are respected;
  9. an empty qualified set refuses.
- `cmr spawn` with a fake `task-board` binary on `PATH` passes the exact args.
