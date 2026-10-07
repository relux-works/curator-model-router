# Usage facts and headroom-aware distribution: specification

Status: **DRAFT**, 2026-10-01.

**Source decisions:** the operator decisions of 2026-09-30 (an operator ruling):

- D1: usage records are facts owned by agents-management, and the router consumes them;
- D2: no CodexBar dependency;
- requirement 2: headroom-aware distribution.

**Inputs:**

- `wiki/research/2026-10-01-usage-limits-awareness.md` (*L1*), for how each harness exposes usage;
- the task-board design for provider quotas (`providerquota`) reviewed on 2026-09-08;
- LP-D9, the availability block and its advisory rule.

---

## 0. Goal

The system should know at any moment how much of each subscription remains, per `(runtime, managed home)`, and use that to spread work:

- prefer capacity that would otherwise expire unused at its next reset;
- conserve a subscription that is burning faster than its window allows;
- keep a reserve for orchestrators and reviewers;
- spread concurrent runs across subscriptions.

**Hard filters are unchanged:** operator admission plus the reactive limit plane (`providerlimits`). Headroom orders candidates and never admits or removes one.

---

## 1. Owners (operator decision D1)

| Part | Owner | Where |
| --- | --- | --- |
| Native probes (a plan plus a pure parser per harness), the record format, the on-disk store and its lock | agents-management | `pkg/providerquota`: the provider-quota design Lane 1, workstream **W3** |
| The executor that runs a plan (TTL, lock, empty scratch cwd), the `provider_quota()` query, the preflight `quota` block | task-board | board-cli `internal/spawn`: Lane 2, part of **W5** |
| Push updates from hosted sessions | session host, writing providerquota records | designed in **W4**, then built |
| Freezing usage facts and in-flight counts into the `CandidateSnapshot` | the caller adapter (task-board, `curator-run`) | **W4** / **W5** |
| The headroom term, the reserve and the spreading | this repository | the `headroom-aware` strategy, workstream **W1** |

**No CodexBar (D2).** CodexBar is not a binary dependency, not a cross-check reader, and not a source of credential paths. It stays prior art (L1 §2).

**Credentials are the harness's business.** No credential file, Keychain item, cookie or browser session is read by any of this code. Only the harness reads its own credential (the provider-quota design principle).

---

## 2. Native surfaces

From L1 §1, verified 2026-10-01. Each harness exposes a pull read and, where it exists, a push in a hosted session.

| Runtime | Pull read (probe) | Push in a hosted session | Grade |
| --- | --- | --- | --- |
| codex | app-server `account/rateLimits/read` (the frozen plan of the EPIC handoff §3) | `account/rateLimits/updated`; `rate_limits` in `TokenCountEvent` | exact, declared schema |
| claude | `claude -p --output-format json --no-session-persistence --setting-sources "" --strict-mcp-config "/usage"`, plus the `usage_report` twin when the CLI attaches it | `rate_limit_event` in stream-json | percent only; exact with `usage_report` |
| muse | MSP `usage/read` over `muse serve` (1.4.1) | MSP `usage/changed` | last observed |
| agy | `agy --output-format json --mode plan --print-timeout 2m --print="/usage"` | none | observed schema |
| gemini | none without reading its credential file | none | `not_supported` |

**Muse.** The `usage/read` row replaces decision D5 of the EPIC (Muse `not_supported`), which was made on Muse 1.0.3, before the method existed.

**No inference.** A probe never makes an inference call. Whether a Muse read needs one `session/start` when `usage` is absent is the empirical check of L1 §5. W3 decides it with evidence.

---

## 3. The record: the producer contract

One record per `(runtime, managed home)`. The router-side projection (§3.1) reads these fields; the producer may carry more.

```jsonc
{
  "identity": "9f1c2ab73d40e5b8",  // providerlimits Identity.Key, or "runtime-<id>" when the harness declares no home
  "runtime": "codex",
  "state": "exact",                // exact | percent_only | last_observed | not_supported | unavailable
  "authenticated": true,           // from the read itself, never from a file's existence; "unknown" when unproven
  "plan": "pro",                   // vendor plan or tier id, verbatim
  "windows": [
    {"id": "session", "scope": "all", "minutes": 300,   "used_percent": 28, "resets_at": "2026-10-01T17:15:00Z", "observed_at": "2026-10-01T12:00:00Z"},
    {"id": "weekly",  "scope": "all", "minutes": 10080, "used_percent": 59, "resets_at": "2026-10-05T17:00:00Z", "observed_at": "2026-10-01T12:00:00Z"}
  ],
  "credits": {"balance": 12.5, "unit": "credits", "unlimited": false},  // optional, when the harness reports it
  "observed_at": "2026-10-01T12:00:00Z",  // the OLDEST window measurement time
  "retrieved_at": "2026-10-01T12:00:03Z", // when the probe or push delivered this record
  "ttl_s": 600,
  "source": "codex-app-server",    // codex-app-server | claude-print-usage | msp-usage-read | agy-print-usage | session-push
  "failures": [],                  // the latest failed reads or pushes, each with its time
  "digest": "sha256:…"             // over the parsed windows only
}
```

**Windows.**

- Window ids and scopes are the vendor's, verbatim.
- `used_percent` may exceed 100: Muse documents values over 100 as valid.
- A model-scoped window maps to registry models through a versioned scope map (§3.1). Its source is the mapping table of the provider-scope mapping design once that exists.

**Every window carries its own `observed_at`: the SOURCE's measurement time, never the retrieval time.**

| Source | `observed_at` |
| --- | --- |
| The harness states when it measured (Muse `observedAtMs`; the timestamps of Claude's `usage_report`) | that time |
| The harness serves live vendor data by contract (Codex `account/rateLimits/read`) | the read time |
| The harness may serve cached data without saying when (Claude `/usage` falls back to bars loaded within the past 60 minutes when its usage request fails, L1 §1.2) | the parser must detect the fallback. When it cannot establish the measurement time, the window carries no `observed_at` and is `invalid`, hence unknown. |

`retrieved_at` records when the record was delivered. Freshness never reads it.

**A partial update never refreshes a window it did not observe:**

- A successful pull read replaces the whole window list. Every window it returns carries its own source time, and a window it no longer returns is dropped.
- A push updates only the windows it carries, each with its own observation time. Untouched windows keep theirs.
- A failed read or push changes no window and no `observed_at`. It is appended to `failures` with its time, so a cached record keeps both the failure and the original ages.

**What the router never receives.**

- No path, hostname, account address or token.
- The producer's `home_display` stays out of the router-facing projection and out of every decision record.

### 3.1 The router-side projection

The caller adapter freezes records into the `CandidateSnapshot`. This projection is the only usage input of the strategy (W1); how task-board produces it is W4's design and W5's code.

```text
CandidateSnapshot (usage members)
    as_of               UTC Unix seconds; sub-second parts are truncated toward negative infinity
    scope_map           {version, entries[{runtime, scope, model_ids[]}]}; an empty map leaves every model-scoped window unmapped
    usage_facts[]       one per subscription key, ordered by key
        key             the record's identity (opaque)
        runtime
        state           exact | percent_only | last_observed | not_supported | unavailable | absent
        windows[]       ordered by id: {id, scope, minutes, used_bp, resets_at?, observed_at?, freshness}
        credits?        {balance, unit, unlimited}: shown by explain, never an ordering input
        record_digest
        failure_count
    inflight[]          {key, runs}, ordered by key
    candidates[]        each with its billing class and, for subscription billing, its usage key
```

**Units.** `used_bp` is `used_percent` in basis points, rounded half to even at freeze time. Timestamps are UTC Unix seconds. All integers are 64-bit.

**Supported range.** Every timestamp (`as_of`, `observed_at`, `resets_at`) lies between 946684800 (2000-01-01) and 7258118400 (2200-01-01), and `ttl_s` lies between 1 and 86400. A snapshot whose `as_of` is outside the range is refused. A window with a timestamp outside it is `invalid`. Within these bounds no subtraction or product can overflow.

**Validation.** The adapter marks each window's `freshness`:

| Freshness | When |
| --- | --- |
| `invalid` | any of: `minutes` outside 1–527040; `used_bp` outside 0–1000000; `observed_at` absent; `observed_at > as_of`; `observed_at` or `resets_at` outside the supported range below |
| `expired` | `resets_at ≤ as_of`: the window has reset since it was observed, so its new state is unknown until a refresh |
| `fresh` | `as_of − observed_at ≤ ttl_s` |
| `stale` | otherwise |

---

## 4. Freshness

- **Only a `fresh`, valid, unexpired window is known.** A `stale`, `expired` or `invalid` window is unknown (operator ruling: stale means unknown). Its values stay in the snapshot for `explain`, and it feeds no ordering key.
- **A read failure is never headroom.** A record with state `not_supported` or `unavailable`, or with no record at all, has no known window.
- **The clock belongs to the caller.** The adapter reads the clock once, freezes `as_of`, and marks freshness. The selector has no clock (R6), and replay uses the frozen `as_of`.

**Refresh.**

- The caller schedules a refresh after a decision when a record is not fresh, never inside the spawn gate (EPIC D3, D8).
- The default TTL is 10 minutes, with one pull read per `(runtime, home)` per TTL (EPIC D7).
- Push updates from hosted sessions keep records fresh without polling.

---

## 5. The headroom-aware strategy

`headroom-aware` wraps a base strategy (`config-order`, `quality-first` or `cost-with-quality-floor`, R6). It never changes what quality is chosen. It changes only which subscription serves it, or which of several operator-declared equivalent pairs.

### 5.1 Interchangeable groups

**The base contract.** The base strategy returns either a total order of all eligible candidates, positions 0 to n−1, or an abstention. When the base abstains, `headroom-aware` abstains with the base's reason.

**The permutation.**

- Headroom permutes candidates only inside an interchangeable group.
- A group's slots are the base positions its members hold; they are immutable.
- Each group is permuted independently over its own slots. Candidates outside every group keep their positions.

**Groups are disjoint,** so the result is unique:

| `equivalence` | Group | Disjointness |
| --- | --- | --- |
| `same-pair-any-home` (default) | candidates with the same runtime, model and effort that differ only in managed home | by construction |
| `declared-groups` | the operator lists groups of pairs it treats as equivalent; nothing else joins a group | checked when the policy loads: an overlap after expansion refuses the policy (`headroom_groups_overlap`), before any decision |
| `fitness-band` (with an estimator) | candidates the estimator assigns to one band; the estimator's output is a partition and cites its version | by the estimator contract |

**Only subscriptions are permuted.** Within a group, only `subscription`-billed candidates move. `metered` and `local` members keep their slots, because their cost is the base strategy's cost term (R6).

### 5.2 Per-candidate quantities

All arithmetic is 64-bit integer, in basis points (bp: 10000 = 100 %), so replay is exact on every platform.

**Applicable windows** are the fact's windows with scope `all`, plus model-scoped windows that the scope map maps to the candidate's model. The policy's `windows` filter applies next. An unmapped model-scoped window is ignored and adds `quota_scope_unmapped`.

For each **known** applicable window `w` (§4):

| Quantity | Definition |
| --- | --- |
| remaining | `r_w = max(0, 10000 − used_bp_w)` |
| time left | with `d = min(resets_at_w − as_of, minutes_w · 60)`: `τ_w = floor(d · 10000 / (minutes_w · 60))`. Absent when `resets_at` is absent. `d` is positive, because a known window has not expired. |
| slack | `s_w = r_w − τ_w`, in −10000…10000; only when `τ_w` is present |

The largest intermediate value is `527040 · 60 · 10000 ≈ 3.2 · 10^11`, well inside 64 bits.

For the candidate:

| Quantity | Defined when | Value |
| --- | --- | --- |
| over | always | 1 if some known applicable window has `used_bp ≥ 10000`, else 0 |
| headroom `H` | at least one applicable window, and every one of them known | `min_w r_w`: the tightest window binds |
| slack `S` | `H` is defined and every applicable window has a `τ_w` | `min_w s_w`: a weekly window that burns fast outranks a roomy session window |
| band `B` | always | `floor(S / band_width_bp)`, rounding toward negative infinity (not Go's truncating division), when `S` is defined; otherwise 0 |

**Incomplete facts are neutral.** A fact is incomplete when:

- no window applies (`quota_no_applicable_window`);
- some applicable window is stale, expired or invalid (`quota_stale`, `quota_expired`, `quota_invalid`);
- only some windows are known (`quota_partial`);
- the record is absent or unreadable (`quota_unknown`).

An incomplete fact gets `B = 0` ("as if on pace"), no `H`, and no reserve. Its only possible effect is `over`, from a window that is known. A partial read therefore never earns a positive preference, and a stale value can never make a candidate look full or empty.

### 5.3 Order inside a group

Sort ascending by this key, then place the group's members into the group's slots in that order:

1. **over.** An over-quota candidate goes last but is not removed: the reactive plane decides `Limited`, because only a launch establishes that (L1 §3.1).
2. **reserve:** 1 if the role is not protected, `H` is defined and `H < reserve_bp`; else 0. Protected roles (default: orchestrator and reviewer) ignore the reserve.
3. **−B:** the higher band first. Capacity that would expire at reset is used before capacity being conserved.
4. **in-flight runs:** fewer first. The count is the queued and running spawns on that `(runtime, home)` at `as_of`, supplied by the caller; the router keeps no counter (R8). A key the caller did not supply counts 0 and adds `inflight_unknown`.
5. **base-order position.**
6. **candidate id**, lexical.

Every step that decides a comparison adds a reason code:

- `quota_over`, `quota_reserve`;
- `quota_expiring` for `B ≥ expiring_band`, `quota_on_pace`, `quota_conserve` for `B ≤ conserve_band`;
- the incompleteness codes of §5.2;
- `inflight_spread`.

Credits are shown in `explain` and are not an ordering input in this version.

**If every candidate is in the reserve zone,** the policy chooses:

- `rank` (the default): the order stands and `reserve_breached` is recorded;
- `abstain`: the strategy abstains, and `select` launches nothing (R7).

### 5.4 Policy parameters

These are part of the policy digest (R8):

```text
headroom
    enabled               default false; W5 turns it on in shadow mode first
    equivalence           same-pair-any-home | declared-groups | fitness-band
    groups[]              for declared-groups: lists of {runtime?, model, effort}
    protected_roles[]     default [orchestrator, reviewer]
    reserve_bp            default 2000
    band_width_bp         default 1000
    expiring_band         default +3
    conserve_band         default -2
    windows               all (default) | a list of window ids
    on_all_reserved       rank (default) | abstain
```

The policy is validated when it loads, never per decision. A violation refuses the policy:

- `band_width_bp ≥ 1`;
- `0 ≤ reserve_bp ≤ 10000`;
- `expiring_band > conserve_band`;
- disjoint declared groups.

---

## 6. What this never does

- It never runs a probe, starts a process, reads a clock, keeps a counter or reserves capacity.
- It never removes a candidate on headroom: over-quota and reserve only move a candidate within its group, and abstain is an explicit policy choice.
- It never switches accounts or routes. Another managed home is another whole candidate (another runtime binding), and choosing it is choosing a variant, never editing one (R7, R11).
- It never changes admission. The admitted set is the same whatever the usage says. This is the byte-identity guarantee of the provider-quota design, kept on the task-board side.

---

## 7. Knowing the remaining capacity at any moment

The operator's view is not the router's. It is:

- `task-board q 'provider_quota()'`: cached records with their age;
- `provider_quota(refresh=true)`: a live read, one per `(runtime, home)` per TTL;
- the preflight `quota` block (Lane 2, W5).

Hosted sessions keep the records fresh by push once W4's seam is built:

- Codex app-server threads through `account/rateLimits/updated`;
- Muse through `usage/changed`;
- Claude child runs through `rate_limit_event`, once the spawner reads stream-json.

The router's own view is `cmr headroom explain --snapshot FILE`. It prints each candidate's windows with their freshness, `H`, `S`, band, class and reason codes.

---

## 8. Acceptance (workstream W1, the strategy)

Test vectors, each replayed from frozen inputs with no clock:

1. **Expiring first.** Two homes for one pair have equal, roomy weekly windows. The one whose session window is 20 % used and resets in 30 minutes ranks before the one that is 20 % used with 4.5 hours left.
2. **The weekly window binds.** A session window with large slack does not win when the weekly window of the same record has negative slack below the other home's.
3. **Stale, expired and invalid are neutral.** Such a fact ranks with band 0, has no reserve effect and no `over` from those windows, and shows its values in `explain`. The same holds for a window observed after `as_of`, and for a window whose parser could not establish a measurement time.
4. **Partial facts earn nothing.** A fact with one fresh roomy window and one stale window gets band 0 and no `H`.
5. **Sparse pushes.** A push that refreshes the session window leaves an untouched weekly window stale, and the fact stays incomplete.
6. **Over goes last and stays.** An over-quota candidate is last in its group and still present.
7. **Reserve by role.** A developer candidate below the reserve goes after its group's non-reserve members. A reviewer candidate ignores the reserve.
8. **Spreading.** With equal bands, the home with fewer in-flight runs wins.
9. **Only the group moves.** Metered and local candidates, and candidates outside every group, keep their slots. Overlapping declared groups refuse the policy.
10. **Scopes.** A model-scoped window without a mapping is ignored with its reason code. A fact with no applicable window is incomplete.
11. **All reserved.** `rank` and `abstain` behave as declared. A base abstention passes through.
12. **Integer and deterministic.** A slack of −1 bp is band −1. The extreme `minutes`, `used_bp` and timestamp bounds compute without overflow, and an `as_of` outside the range refuses the snapshot. Fractional percentages round half to even. The same inputs produce the same order on every platform, and the golden files are committed.
