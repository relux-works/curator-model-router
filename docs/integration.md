# Connecting an orchestrator to task-board

Replace a launch such as `task-board spawn TASK --role developer --background`
with `cmr spawn --task-class code.implement -- TASK --role developer --background`.
Put the task brief in the board task description, with scope, acceptance criteria, checklist, and precondition resources. Task-board combines this board state with the role template to build the agent prompt. Use `--instruction RESOURCE` to select a precondition resource, or the supported legacy `--task-path FILE` for an additional brief. Include `--background` to launch a tracked run.

Supply a task class on the cmr side; the role may be supplied there or in the forwarded arguments.
In select/recommend modes, a role supplied only to cmr is also forwarded to task-board.
An explicit difficulty wins over the task-class default. `--delicate` requests the strongest qualified
configuration; `--fanout` launches up to three A-or-better configurations from distinct families.
The embedded researched catalog currently has only two such families at the default edges.

In select/recommend modes, the caller's `--agent`, `--model`, and `--reasoning-effort` flags are locks, including `--flag=value`. Shadow ignores these forwarded selection locks for its advisory decision; cmr-side flags before `--` still constrain it.
Values of other task-board options are consumed according to their flag arity, including flag-looking strings and literal `--` values. Unknown forwarded options refuse so their arity cannot be guessed.
cmr fills missing flags and forwards board selectors to every preflight query. It appends `--selection-rationale 'cmr:<decision-id> ...'` only for v2 ordered criteria or v3/v4 `adjustment_confirmation = "required"`. Equal/absent criteria and confirmation `none` forbid that flag. Caller rationale values remain unchanged; candidate files and PATH discovery supply no confirmation policy, so cmr adds no rationale in those cases. The decision ID remains in cmr output and the local decision log.
Task-board's empty effort for a model without an effort axis maps to the catalog's `none` row;
cmr omits `--reasoning-effort` when printing or injecting that configuration.
Conflicting or duplicate selection flags are typed refusals. An empty qualified set returns
`no_qualified_candidate` and launches nothing in select/recommend modes. Shadow still launches the original caller arguments. Each launch still passes through
task-board's existing spawn gate; task-board output and exit status pass through unchanged.
Fan-out invokes the gate sequentially once per selected configuration and stops on the first failure;
already launched runs remain launched.

## Task-class aliases

Board task classes and workload classes map deterministically to the router taxonomy:

| Caller class | Router class |
| --- | --- |
| `implementation`, `code`, `unified` | `code.implement` |
| `debugging` | `code.fix` |
| `testing` | `code.test` |
| `migration` | `code.refactor` |
| `review` | `review.code` |
| `documentation`, `docs` | `docs.write` |
| `research` | `research` |
| `architecture` | `planning` |
| `mechanical`, `metadata` | `routine` |
| `operations` | `orchestration` for role `orchestrator`, otherwise `ops` |

Canonical router classes remain accepted. Unknown classes return `invalid_task`.
The wrapper has no underlying board-class context for `unified` or `review`, so it
uses the explicit defaults above. Pass `review.spec`, `orchestration`, `routine`,
`docs.write`, or `research` directly when those contextual classes apply.
Decisions store the supplied `inputs.task.original_task_class` and the mapped
`inputs.task.task_class`; shadow observations also retain both values. Alias
provenance is part of the decision content ID and survives replay.

## Candidate and catalog overrides

Admission comes from `--candidates FILE` (a JSON array of `{runtime, model, effort}`), then
a role-only `task-board --no-update-check q 'project_config(view=spawn-preflight, role=R)'`.
When its response already contains complete provider admission, cmr reuses it. Missing
provider data triggers targeted `agent=A` queries with at most four concurrent subprocesses;
provider order determines output and refusal ordering. All queries share one deadline. cmr intersects the role ceiling's canonical `admitted_pairs.models[].{id,efforts}`
with `workload_class_recommendation.available_pairs[].{runtime,model,reasoning_effort}` when available.
Each provider response must contain its resolved role ceiling and boolean `configured` field. Only explicit `configured = false` admits all catalog rows of an allowed runtime. Missing, null, or incomplete ceiling evidence returns `invalid_admission`. A disabled board or an empty available set stays empty. Unreadable/indeterminate preflight refuses;
it never silently broadens admission. Role-only queries use canonical admission only for explicit unconfigured workload policy or a well-formed `workload_class_derivation_input_required` state. A missing legacy workload block is accepted only with an explicitly unconfigured ceiling. A resolved workload must include `configured`, `class_resolved`, determinate integrity, and a non-null available-pair array; an empty array admits nothing. Task-board rechecks live availability at spawn.
`--preflight-timeout 90s` overrides policy `preflight_timeout_seconds = 90`.
The default is 60 seconds for the whole preflight operation, including fallback queries.
Policy zero/omission means the default; explicit CLI durations must be positive, at most 24h.
Timeouts return `preflight_timeout`; subprocess failures return `preflight_failed`;
malformed or incomplete authority still returns `invalid_admission`. Provider output is discarded.

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
preflight_timeout_seconds = 60
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

## Shadow observations and reports

Run shadow for one week before switching an orchestrator to select, then read the divergence
summary for the founders:

```sh
cmr spawn --task-class code.implement --mode shadow -- TASK --role developer --agent codex --model gpt-6.1-sol --reasoning-effort high --background
cmr shadow report
cmr shadow report --since 2026-10-07T00:00:00Z
cmr shadow report --since 2026-10-07T00:00:00Z --json
```

Shadow starts task-board with the caller's exact argv before advisory parsing,
preflight, selection, decision storage, or logging. An explicit `--mode shadow`
also defers policy reads until after child startup. When mode comes from policy,
reading that policy is necessary to determine execution mode before launching.
Advisory work runs concurrently with its own deadline (`--advisory-timeout 60s`,
60 seconds by default). After the child exits, cmr gives unfinished advice up to
250 milliseconds to finish, within its existing deadline. It then cancels remaining
work and records `advisory_timeout`. Advisory subprocesses use separate process
groups; cancellation terminates the entire group, allowing 100 milliseconds for
graceful exit before force-killing it. Cleanup and reaping share a one-second bound
before cmr returns. A completed preflight timeout
retains `preflight_timeout`. Advisory output and observations are written after
the child finishes, and the wrapper returns the child's status even on relay or
storage failures. Signal termination uses the shell status `128 + signal` in both
the wrapper and observation (SIGPIPE is 141, SIGTERM is 143). SIGINT and SIGTERM
addressed to cmr are forwarded to the launched child. Timeout observations retain
the loaded policy's budget, including an explicit `--budget` override; if policy
loading has not completed, the budget is `unknown`. Failed advice never references
an unsaved decision. Advice exceeding the grace period still produces a timeout
observation.

Shadow computes the advisory pick without locking to forwarded `--agent`, `--model`, or
`--reasoning-effort`. Forwarded `--role` still defines admission, and conflicts with a different
cmr-side role are advisory errors. Explicit cmr-side selection flags before `--`, admission,
policy, budget, and tier floors still apply. The actual spawn argv is preserved byte for byte;
shadow launches once even when the advisory decision uses fan-out. `cmr_pick` records its primary
selected configuration, the first fan-out configuration; the decision record retains the full fan-out.

After the child completes, shadow appends one JSON line to
`$XDG_STATE_HOME/curator/model-router/shadow/observations.jsonl` (default
`$HOME/.local/state/curator/model-router/shadow/observations.jsonl`). Observation storage uses
private directory/file permissions and a single append write per line. A storage failure prints
`cmr:shadow warning: could not append observation` on stderr and preserves launch and exit status.
A failed append leaves no observation to count; monitor these warnings during the shadow week.

The stable `shadow-observation-v1` JSON fields are:

| Field | Meaning |
| --- | --- |
| `schema_version` | `shadow-observation-v1` |
| `time` | UTC RFC3339 timestamp after child completion, possibly fractional seconds |
| `decision_id` | Content ID, or `null` when none was computed; may exist even if decision storage failed |
| `original_task_class` | Supplied class, before alias translation; older records may omit it |
| `role`, `task_class`, `difficulty`, `sensitivity`, `budget` | Effective advisory profile and budget; on early failures, available input values and resolvable defaults |
| `cmr_pick` | `{runtime, model, effort}` on success, or `{code}` for refusal/advisory error |
| `caller_pair` | `{runtime, model, effort}` from explicit forwarded flags; absent dimensions are `null`, meaning board defaults; no defaults are guessed |
| `agreement` | One of the classes below |
| `task_board_exit_code` | Preserved completed child status; start failures refuse without an observation |
| `cmr_error` | Present only on advisory failure: `{code, message}` with a typed code and fixed sanitized message; no provider diagnostics, paths, or raw argv |

If forwarded parsing fails, only flags parsed safely before the error are available for the
observation. Arguments after task-board's own `--` are literal arguments, not selection flags.
The complete launch still passes through unchanged.

Agreement classification checks absence of a cmr pick first, then any missing caller dimension,
then runtime, model, and effort. Registry model aliases are canonicalized for comparison while the
recorded caller values and launched arguments stay unchanged:

| Class | Meaning |
| --- | --- |
| `exact` | All three explicit caller dimensions match the pick |
| `same_model_other_effort` | Runtime and model match; effort differs |
| `same_runtime_other_model` | Runtime matches; model differs |
| `different_runtime` | Runtime differs |
| `unknown_caller_default` | A pick exists but at least one caller dimension is `null` |
| `no_cmr_pick` | Refusal or advisory error, never agreement |

Human report output starts with observation/comparison/agree/diverge totals, then counts unknown
caller defaults, refusals, FAIL-OPEN launches, and nonzero task-board exits. It shows the agreement
distribution, all divergence groups ordered by count, refusal counts, FAIL-OPEN counts grouped by code, and a separate **FAIL-OPEN
launches** list of advisory errors. A comparison requires a successful pick and three explicit
caller dimensions. Its denominator excludes unknown defaults, refusals, and advisory errors.
A nonzero task-board exit alone does not imply an advisory failure or refusal.

`--since` is inclusive and accepts RFC3339 with an offset or fractional seconds; report JSON
normalizes it to UTC. Missing observations produce an empty successful report. Unreadable,
malformed or unsupported-schema complete lines cause a typed report error; they are never
silently counted as agreement. Only the final record may be unterminated (an interrupted
append): a complete record that lost just its newline still counts, while a partial final
record of any size is excluded, `totals.truncated_tail` is `true`, and the human report prints
a warning. A concurrent append after the report reaches EOF
is included on the next report invocation.

`--json` emits stable `shadow-report-v1`:

```json
{
  "schema_version": "shadow-report-v1",
  "since": null,
  "totals": {
    "observations": 0, "comparisons": 0, "agreements": 0, "divergences": 0,
    "unknown_caller_defaults": 0, "refusals": 0, "fail_open_launches": 0,
    "nonzero_task_board_exits": 0,
    "truncated_tail": false
  },
  "agreement_distribution": {
    "exact": 0, "same_model_other_effort": 0, "same_runtime_other_model": 0,
    "different_runtime": 0, "unknown_caller_default": 0, "no_cmr_pick": 0
  },
  "top_divergences": [],
  "refusals": [],
  "fail_open_by_code": [],
  "fail_open_launches": []
}
```

Every distribution key is always present. `top_divergences` entries contain
`{role, task_class, difficulty, caller_pair, cmr_pick, count}` grouped by those five dimensions;
they include all groups in descending count order, with serialized JSON group keys breaking ties.
`refusals` entries are `{code, count}`, ordered by descending count then code. Advisory errors
are excluded from refusals, grouped in `fail_open_by_code` using the same ordering, and appear as full observations in `fail_open_launches`, in file order.
Empty arrays remain `[]`, and omitted `--since` is `null`. No generated report timestamp is added,
so unchanged observations and arguments produce identical JSON.

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
