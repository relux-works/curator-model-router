# Connecting an orchestrator to task-board

Replace a launch such as `task-board spawn TASK --role developer --background`
with `cmr spawn --task-class code.implement -- TASK --role developer --background`.
Put the task brief in the board task description, with scope, acceptance criteria, checklist, and precondition resources. Task-board combines this board state with the role template to build the agent prompt. Use `--instruction RESOURCE` to select a precondition resource, or the supported legacy `--task-path FILE` for an additional brief. Include `--background` to launch a tracked run.

Supply a task class on the cmr side; the role may be supplied there or in the forwarded arguments.
In select/recommend modes, a role supplied only to cmr is also forwarded to task-board.
An explicit difficulty wins over the task-class default. `--delicate` requests the strongest qualified
configuration; `--fanout` launches up to three A-or-better configurations from distinct families.
The embedded researched catalog currently has only two such families at the default edges.

The caller's `--agent`, `--model`, and `--reasoning-effort` flags are locks, including `--flag=value`.
Values of other task-board options are consumed according to their flag arity, including flag-looking strings and literal `--` values. Unknown forwarded options refuse so their arity cannot be guessed.
cmr fills missing flags and forwards board selectors to every preflight query. It appends `--selection-rationale 'cmr:<decision-id> ...'` only for v2 ordered criteria or v3/v4 `adjustment_confirmation = "required"`. Equal/absent criteria and confirmation `none` forbid that flag. Caller rationale values remain unchanged; candidate files and PATH discovery supply no confirmation policy, so cmr adds no rationale in those cases. The decision ID remains in cmr output and the local decision log.
Task-board's empty effort for a model without an effort axis maps to the catalog's `none` row;
cmr omits `--reasoning-effort` when printing or injecting that configuration.
Conflicting or duplicate selection flags are typed refusals. An empty qualified set returns
`no_qualified_candidate` and launches nothing in every mode. Each launch still passes through
task-board's existing spawn gate; task-board output and exit status pass through unchanged.
Fan-out invokes the gate sequentially once per selected configuration and stops on the first failure;
already launched runs remain launched.

## Candidate and catalog overrides

Admission comes from `--candidates FILE` (a JSON array of `{runtime, model, effort}`), then
`task-board --no-update-check q 'project_config(view=spawn-preflight, role=R, agent=A)'` for each
allowed agent. cmr intersects the role ceiling's canonical `admitted_pairs.models[].{id,efforts}`
with `workload_class_recommendation.available_pairs[].{runtime,model,reasoning_effort}` when available.
The resolved role ceiling and its boolean `configured` field must be present. Only explicit `configured = false` admits all catalog rows of an allowed runtime. Missing, null, or incomplete ceiling evidence returns `invalid_admission`. A disabled board or an empty available set stays empty. Unreadable/indeterminate preflight refuses;
it never silently broadens admission. Role-only queries use canonical admission only for explicit unconfigured workload policy or a well-formed `workload_class_derivation_input_required` state. A missing legacy workload block is accepted only with an explicitly unconfigured ceiling. A resolved workload must include `configured`, `class_resolved`, determinate integrity, and a non-null available-pair array; an empty array admits nothing. Task-board rechecks live availability at spawn.
If task-board is absent from PATH, cmr uses catalog rows whose runtime binaries resolve on PATH
and records `admission_source: path-catalog` in the output and decision.

Catalog lookup: `--catalog FILE`, then `$XDG_CONFIG_HOME/curator/model-router/catalog.json`
(`$HOME/.config` when XDG_CONFIG_HOME is unset), then `recommend.DefaultCatalog()`'s embedded catalog.
An explicit missing or malformed override refuses. Catalog rows do not themselves grant admission. The task platform defaults to the local Go OS name (for example `darwin`, `linux`, or `windows`); `--platform NAME` on the cmr side overrides it. Catalog `not_for` constraints filter that platform before selection and spawning.

## Policy and standing rules

Policy lookup: `--policy FILE` (TOML or JSON), then `$HOME/.curator/routing.toml`, then built-in defaults.
Missing implicit files use defaults; explicit missing files refuse. TOML is decoded with
`github.com/pelletier/go-toml/v2`, then passed through the library's strict policy overlay and validation.
Unknown fields and invalid values refuse. Defaults include:

```toml
schema_version = "recommend-policy-v1"
mode = "select"
budget_mode = "balanced"
burn_fanout_cap = 4
allow_metered = false
standard_a_max_cost_ratio = 1.5
interchangeable_cost_bp = 2500
fanout_k = 3
[tier_edges]
s = 62
a = 56
b = 45
[review_tier_edges]
s = 40
a = 30
b = 20
[difficulties.trivial]
minimum_tier = "C"
objective = "cheapest"
[difficulties.routine]
minimum_tier = "B"
objective = "cheapest"
[difficulties.standard]
minimum_tier = "B"
objective = "standard"
[difficulties.hard]
minimum_tier = "A"
objective = "quality"
[difficulties.critical]
minimum_tier = "S"
objective = "quality_ignore_cost"
[headroom]
enabled = true
reserve_bp = 2000
```

Omitted headroom settings keep `recommend.DefaultPolicy()` defaults. Copy
[standing-rules.toml](standing-rules.toml) to the operator policy location to apply the documented
example operator rulings, and then make it yours before relying on it:

- replace every `<your-...-host>` selector with the real short hostname (host matching is exact, so an
  unreplaced placeholder silently skips the rule), including the host in the cross-provider exception;
- set or delete the `<STORY-ID>` example;
- run `cmr recommend` once and check that the printed `rule=... source=...` lines list the rulings you expect.
 Each applied rule is printed with its ID, source and rationale and stored in the decision.
Use `--host` to override policy `host`; otherwise the short hostname is used. Pass `--story`,
`--producer-family`, and repeatable `--exclude runtime/model` to activate their rule facets.
These rules filter already admitted candidates and never bypass a difficulty floor or caller lock.
`rules.when.difficulty` and `rules.when.sensitivity` are optional nonempty lists of task
values; omitted lists match any task. The host Astra medium pin applies only to normal
trivial/routine/standard reviews. Demanding reviews retain the Codex preference and pick
the best qualified configuration, preferring Codex within the same best-quality band.
The preference explicitly scopes all difficulties; only its own matching scope enables
this tie behavior. An unrelated or skipped rule cannot change ranking.
Skipped scoped rules name the mismatching facet in the explanation.

The public catalog uses pinned Bug Hunt measurements. code.*, tool-use and ops have a coding-index primary, review.* a review-measurement primary, and other classes an overall-index primary. Missing primaries use the Bug Hunt measurement; missing measurements leave quality unknown and refuse selection. Primary rows rank ahead of fallback rows in every budget, with no numeric comparison across scales. Index edges are `tier_edges`; all measured selections use `review_tier_edges`. Explanations name the chosen source and include `quality_from_bughunt_fallback` for non-review fallback selections.

An operator can add indices or replace individual quality/cost fields with strict overlay-v1 JSON. Set `catalog_overlay = "./operator-overlay.json"` in policy or pass `--catalog-overlay FILE` to recommend/spawn; the flag wins. No implicit overlay lookup occurs. Relative paths use the process working directory. Unknown configurations and malformed files refuse. Every scalar has its own kind/source/as_of. The decision freezes the overlay, effective catalog and canonical overlay digest; replay never reads the file. See [the complete format and fictional example](../spec/recommend.md#11-explicit-operator-overlays) and [catalog default picks](../catalog/NOTES.md).

## Spending policy

Both `recommend` and `spawn` accept `--budget economy|balanced|burn`, overriding policy
`budget_mode` for this decision. Balanced is the default and retains existing selection.
Economy chooses the cheapest qualified configuration at every difficulty and skips the
standard A upgrade; hard/critical/delicate floors still apply. Burn maximizes quality at
every difficulty, spends the most remaining known subscription headroom inside quality
ties, and fans out to every qualified A+ family up to `burn_fanout_cap` (default 4).
Admission, locks and billing rules still apply. Usage that is unknown or stale is neutral.
Automatic per-runtime budget selection is deferred; `auto` is rejected.

```sh
cmr recommend --role developer --task-class code.implement --budget burn
cmr spawn --task-class review.code --budget economy --mode recommend -- TASK --role reviewer --background
```

The chosen policy is frozen in the decision and its content ID. Empty/omitted wire
`budget_mode` means balanced for compatibility with existing goldens and records.
Human output prints the effective budget; JSON records non-default modes explicitly.

## Modes and output

`--mode select` invokes task-board with the filled flags (default). `--mode recommend` prints the
complete proposed argv and launches nothing. In both flag and policy modes, **shadow is fail-open**:
`--mode shadow` executes the caller's task-board spawn arguments unchanged even when recommendation fails
(invalid admission, preflight error, refusal, no qualified candidate, policy load error,
or usage error). The would-be decision or typed error is logged on stderr as `cmr:shadow ...`;
completed decisions are also stored. Logging or decision-storage failures cannot block launch.
Only cmr argument parse errors before `--` and a missing task-board binary stop shadow;
arguments after `--` are passed through even if cmr cannot parse them. Task-board owns the
output and exit status. Policy `mode = "shadow"` has the same behavior; a readable shadow
declaration is honored even if other policy fields are invalid. An unreadable policy cannot
declare a mode, so use `--mode shadow` to guarantee fail-open behavior in that case. Policy `mode` supplies
the default when no mode flag is present. `cmr recommend` always computes and prints without spawning.
Human output includes the selection, ranked alternatives, rule sources, exact flags, admission source
and decision ID. `--json` gives structured output for every new command and typed refusal; select/shadow
preserve task-board's own output format and exit code. To request task-board JSON too, forward its `--json`.

Decision records live under `$XDG_STATE_HOME/curator/model-router/decisions/` and contain the frozen inputs
and replayable recommendation. When XDG_STATE_HOME is unset, `$HOME/.local/state` is used.

## Usage

`cmr usage refresh [--runtime codex|claude|muse|agy] [--ttl 10m] [--json]` executes the
skill-agents-management v0.5.40 providerquota Reader plans. `cmr usage show [--json]` reads the cache
without starting a harness. `cmr recommend --refresh` refreshes stale cached reads for admitted runtimes
before freezing the routing clock. TTL must be a whole number of seconds between 1s and 24h.
A cached record's TTL controls its next read; changing `--ttl` does not bypass an existing throttle.

The executor passes the plan's binary, argv, environment and stdin, uses an empty scratch directory,
bounds output to 4 MiB, enforces the timeout, performs the initialize/initialized/read handshake and
closes stdin on Complete. Agy first requires bounded `--version`/`--help` evidence for its usage contract.
No code here opens or lists harness homes or credential files. Home identities use plugin declarations
and lexical environment paths; aliases are deliberately not resolved by probing a harness home.
Plugins without a declared home (Muse/Agy in v0.5.40) use runtime-only keys rather than guessing a home.

An O_EXCL lock covers each entire refresh and commit under
`$XDG_STATE_HOME/curator/model-router/usage/`. Concurrent refresh returns a typed locked refusal.
Failed reads cache a sanitized unavailable record and retain prior measurement ages without supplying
headroom. Routing freshness is classified separately for every window at one frozen `as_of`;
percentages convert with `routing.PercentToBP`. Empty provider scopes become routing's account-wide
`all` scope. Scoped windows map only to exact catalog model IDs;
unrecognized provider group labels remain neutral. Claude prose and Agy payloads without measurement
timestamps also remain neutral. Muse's fresh-host response can be `no_observation`; no session-start,
inference or credential fallback is attempted. JSON output uses the sanitized router projection.

All integration tests use temporary HOME/XDG directories and fake task-board/harness scripts on PATH.
No real task-board, harness, credential file or harness home is needed to run them.
