# Evidence format: specification

Status: **DRAFT**, 2026-10-01. It extends R4 of `spec/model-routing.md` and implements requirement 1 of the operator decisions of 2026-09-30 (an operator ruling). Where they differ, this file governs the shape of evidence and `model-routing.md` governs how the router uses it.

Placement follows decision D1 of that ruling:

- The format, the store, the importers and the derived views live in this repository.
- agents-management keeps the registry facts they refer to: model ids, the per-row bench data in `vendorplugin/bench.go`, and the planned `ExternalIDs` field on a registry row.

This supersedes the placement proposed in the task-board evidence-store design, which put an evidence store in a module package beside `providerlimits`.

---

## 0. Goal

Any benchmark, eval run, review outcome or operator judgement becomes one internal format before the router reads it.

- The estimator, the role-suitability view and `explain` read this format and nothing else.
- Importers are the only code that knows a source's native shape.
- A new source therefore needs a new importer and nothing else.

A record states five things explicitly and never leaves them implied:

1. which benchmark, which version, and which evaluation protocol;
2. which task categories the value speaks for;
3. the exact model and reasoning effort it was measured at;
4. the metric, with unit, direction, sample count, uncertainty, evidence kind, grading method and provenance;
5. for an internal judgement rather than a measurement: who says so, on what basis, how confident they are, and until when.

---

## 1. Documents

Evidence moves in two kinds of document. Both are canonical JSON: the digest rules are those of `model-routing.md` §5.

| Document | Content | Mutability |
| --- | --- | --- |
| `evidence-import` | one import from one source: header, `benchmarks[]`, `observations[]`, `notes[]`, `retractions[]` | immutable once stored; a correction is a new import |
| `evidence-snapshot` | the set of import digests in force, the taxonomy version, and the registry reference used to resolve model ids | immutable; a new import makes a new snapshot |

**Import header:**

- `schema_version`;
- `source`: a stable slug such as `bug-hunt`, `internal-evals`, `run-outcomes` or `operator-notes`;
- `importer {name, version}`;
- `imported_at`;
- `measured_by`: `vendor`, `third_party` or `internal`.

`measured_by` records independence. A vendor-reported number is still a measurement under the vendor's protocol, but it is not independent confirmation.

**Snapshot identity.** An `EvaluationSnapshot` (R3) names exactly one evidence snapshot by digest. Derived views (§4) are computed per snapshot and never imported.

---

## 2. Records

### 2.0 Record identity, replacement and retraction

**Stable addresses:**

- a benchmark is `{id}@{version}`;
- an observation is `obs:` plus the SHA-256 of its canonical form without its `id` field;
- a note is `note:` plus the SHA-256 of its canonical form without its `id` field;
- a retraction is `ret:` plus the SHA-256 of its canonical form without its `id` field.

Contributions, `supersedes`, corrections, `evidence_refs` and `explain` all cite these addresses.

**Replacement and retraction are append-only:**

- An observation or a note may carry `supersedes: [ids]`. Its import is the correction; the superseded records stay in their imports.
- A `retraction {target, reason, author, at}` record withdraws a target without replacing it. A retraction cannot itself be retracted: to restore a withdrawn record, import a new record.

**The active set.** Take every record of the snapshot's imports.

1. A retracted record is withdrawn: it is not active, and it supersedes nothing. Its `supersedes` edges are dropped.
2. The remaining edges form the supersession graph. An import that would close a cycle is refused.
3. A record is **active** when it is not retracted and no path of one or more remaining edges leads to it.

**Consequences.**

- In the chain C → B → A, only C is active.
- Retracting C revives B, and A stays suppressed through B.
- Retracting B leaves C active and revives A, because C named only B. A replacement that means to replace a whole lineage names every record it replaces.

**Status is derived, never stored.** A record's status (`active`, `superseded` or `retracted`) is computed by this rule and shown by `explain` and `cmr evidence show`. No imported field can contradict it.

**Conflicts are reported, not resolved by guess.** When two active records supersede the same target, both stay active. The snapshot reports `evidence_conflict`, and the derivation reads both. This is deterministic, because the active set does not depend on import order.

### 2.1 Benchmark

```text
benchmark
    id                  stable slug: "bug-hunt-bench", "swe-bench-verified", "internal-review-set"
    version             exact version or dataset revision
    publisher           who runs it
    dataset / split     optional
    protocol_digest     digest of the evaluation protocol (scaffold, prompts, limits) when known
    categories[]        task categories it speaks for (§3), each with a one-line coverage note
    metrics[]           {name, unit, direction: higher_better | lower_better, scale?, description}
    grading_method      tests | exact-match | llm-judge | human | mixed
    citation            optional URL or reference
```

**Versions.** A benchmark whose version or protocol changes is a new `{id, version}`. Observations of different versions are never pooled unless a declared mapping says they are compatible.

### 2.2 Observation

One measured or derived value.

```text
observation
    benchmark_ref               {id, version}
    subject
        model_id                registry id (agents-management), e.g. "gpt-6.1-sol"
        runtime                 runtime id when the source fixes it, else absent (= unknown)
        harness_version         when known
        effort                  the effort token as launched, verbatim; "none" for a model that takes none
        engine_profile          local models only (LP-D7 members)
        tool_profile / execution_profile   when known
    subject_resolution          resolved | unresolved
    categories[]                the subset of the benchmark's categories this value speaks for
    facets                      {languages[], platforms[]}  optional
    metric                      a name from the benchmark's metrics
    value
    sample_count                integer, or absent (= unknown)
    uncertainty                 {kind: stderr | ci95 | range, value}, or absent
    evidence_kind               measured | interpolated | transferred
    transfer                    required when transferred: {from: {model_id?, effort?}, rule_id, rule_version}
    supersedes                  optional: [ids] this observation replaces (§2.0)
    grading_method              tests | exact-match | llm-judge | human
    cost                        optional per sample: tokens_in, tokens_out, usd, wall_s; each absent = unknown
    provenance                  {source, retrieved_at, raw_artifact_ref, origin_ref}
    observed_at                 when the measurement was taken, not when it was imported
```

**Effort is exact.**

- `effort` is the token the harness was launched with, verbatim: today `low`, `medium`, `high`, `xhigh` or `max`, or `none`. It is validated against the pinned registry's effort vocabulary for that model (agents-management), not against a list fixed here. A token the vocabulary does not know is kept, flagged `effort_unrecognised`, and never matched.
- A source that does not state an effort yields an absent effort, which means unknown. The vendor's default is never assumed.
- An observation with an unknown effort never matches a candidate's effort.
- A value carried to another effort, or to another model, exists only as `evidence_kind = transferred`. It names its transfer rule and version and carries its own uncertainty (R4).

**Model identity.**

- `model_id` is the agents-management registry id.
- A source's external name maps through the registry row's `ExternalIDs` when that field exists, or through an importer mapping table that carries its own version.
- An unmapped name is kept with `subject_resolution = unresolved` and listed in the snapshot's `unresolved` report. It is never registered, never matched and never dropped.

**Independence.** `provenance.origin_ref` names the original publication or run. Reprints of one result share an `origin_ref` and count once (R4).

### 2.3 Note: an internal observation about a model

A curated judgement, for example "good for review", "good for Go implementation" or "weak on Windows work".

```text
note
    subject             {model_id, runtime?, efforts[]?, harness_version_range?}
    claim
        polarity        strength | weakness | caution
        statement       one short sentence
        categories[]    task categories it concerns (§3)
        facets          {languages[], platforms[], roles[]}  optional
    basis               review-outcomes | run-outcomes | operator-judgement | incident | benchmark-reading
    evidence_refs[]     {kind: run | review | pull-request | board-element | observation | document, ref}
    confidence          low | medium | high
    author              {kind: human | agent, id}
    created_at
    review_by           the date after which the note is stale unless re-confirmed
    supersedes          optional: [ids] this note replaces (§2.0)
```

**Notes are priors, not measurements.**

- A note never becomes an observation and never carries a number that looks like a metric.
- The estimator weighs a note by `confidence` and `basis` with weights declared in the derivation version (§4).
- `explain` shows notes separately from measurements.
- After `review_by` a note is stale. It is shown with its age, and its weight is zero unless the policy says otherwise.

**Notes are never policy.** A note never admits or removes a candidate. "Never use model X for task Y" is an operator policy and belongs in the operator's files (`models.toml`, role ceilings), not in evidence.

**Examples**, in the shape above, drawn from recorded operator practice:

| Subject | Claim | Basis |
| --- | --- | --- |
| `claude-sonnet-5-5` / `high` | strength: reliable worker for implementation and for routine reviews; categories `code.implement`, `review.code` | operator-judgement |
| `gpt-6-astra` / `high` | strength: suited to high-stakes reviews; categories `review.code`; roles `reviewer` | operator-judgement |
| `gpt-6-luna` / `max` | weakness: Windows work repeatedly fails review; platforms `windows` | review-outcomes |

---

## 3. Task categories

A versioned controlled vocabulary, taxonomy **v1**:

| Category | Covers |
| --- | --- |
| `code.implement` | new behaviour, feature code |
| `code.fix` | a fix against a reproduction |
| `code.refactor` | behaviour-preserving change |
| `code.test` | writing or repairing tests |
| `review.code` | reviewing a code change |
| `review.spec` | reviewing a specification or design |
| `docs.write` | documentation and reference pages |
| `research` | investigation from sources, written findings |
| `planning` | decomposition, estimates, plans |
| `orchestration` | coordinating child agents over a long horizon |
| `tool-use` | agentic terminal and tool work |
| `ops` | CI, infrastructure, release chores |
| `routine` | mechanical edits, formatting, translation |

**Facets** refine a category without multiplying the vocabulary:

- `language`: `go`, `swift`, `python`, `typescript` and so on;
- `platform`: `macos`, `ios`, `linux`, `windows`, `android`;
- `context_size`: `small`, `medium`, `large`;
- `horizon`: `short`, `long`.

**Rules.**

- A new category needs a new taxonomy version.
- An import that uses a category unknown to the snapshot's taxonomy is kept. The category is reported, never dropped and never silently mapped.
- The mapping from task-board workload classes and playbook role needs to categories is a declared, versioned table. It is part of the derivation (§4) and not of any import.

---

## 4. Role suitability: a derived view

For each role and candidate, the view says how suitable the candidate is and why:

```text
role_suitability
    role            a playbook role alias: orchestrator, developer, reviewer, researcher, ...
    candidate       the public execution fingerprint of R3: {model_id, effort, runtime, harness_version,
                    engine_profile, tool_profile, execution_profile}; members may be unknown
    grade           strong | adequate | weak | unknown
    score           optional, with its scale
    coverage        per required category: measured | interpolated | transferred | notes-only | none,
                    each marked direct or partial (§4, matching)
    contributions[] {ref: an observation or note, direction: + | -, weight}
    derivation      {name, version, digest}
```

**Inputs:**

- the role's requirements: playbook role needs, mapped to categories and facets with weights through the declared table of §3;
- the snapshot's observations and notes;
- the derivation version, which declares the metric normalisation per benchmark, the note weights and the grade thresholds.

**Matching an observation to a candidate:**

| Member | Rule |
| --- | --- |
| `model_id`, `effort` | must be known on both sides and equal; an unknown effort never matches (§2.2) |
| any other fingerprint member | known on both sides and equal: a match. Known on both sides and different: no match. Unknown on either side, or on both: a `partial` match, whose weight discount the derivation version declares. |

`coverage` records `partial` matches as such, so a measurement at another harness version or engine profile is never shown as a direct one.

**View identity.** A view depends on more than the snapshot, so its identity binds every input:

- the evidence snapshot digest;
- the derivation digest;
- the role-requirements digest: the role needs and the category-mapping table, per project;
- the weights and policy digest;
- `evaluated_at`, the frozen evaluation time that decides note staleness;
- the candidate-set digest: the sorted full fingerprints of the candidates the view covers.

The view id is the digest over those six. Neither note expiry nor a different project's requirements can silently change a view, or reuse one.

**Rules.**

- The view is recomputed for each distinct set of inputs and never edited by hand.
- It is stored under its view id, with its inputs listed inside, so `explain` and replay read the same view.
- A category with no evidence contributes `none` coverage, not a zero score. `unknown` is a grade in its own right.
- The view advises the estimator (R5). It never admits a candidate, and it never reorders across vendors by the module's `Rank.Score`, which the module declares non-comparable across vendors.

---

## 5. Rules carried from R4, and new ones

- An absent field means unknown. Zero is a measured value. A missing price is unknown, not free.
- Leaderboards are never averaged. Two benchmarks are combined only through a declared mapping, with its normalisation and applicability stated.
- An import never mutates an earlier import. A correction is a new import whose records name the ones they supersede.
- The store never writes the registry and never rewrites the module's `Rank.Score`.
- Privacy:
  - raw artifacts are held by reference (`raw_artifact_ref`), never inlined;
  - records carry no secrets, no credentials, no private hostnames or paths, and no personal contact data;
  - `evidence_refs` may name private identifiers such as a run or board id; to the router they are opaque.
- Observations do not expire by themselves. A policy may filter by `observed_at` or by `harness_version`. A harness or model change is a different subject, never an in-place update.

---

## 6. Importers

| Source | Importer | When |
| --- | --- | --- |
| Bug Hunt rows in agents-management `vendorplugin/bench.go` | `bughunt`: reads the module's exported bench data for a pinned module version; keeps `measured` and `interpolated` as the module states them; imports cost rows as `cost` | W2 |
| A file already in this format | `native`: validates, canonicalises, reports unknown categories and unresolved models | W2 |
| Operator and orchestrator notes | `notes`: the CLI `cmr note add` writes a one-note import | W2 |
| Own eval runs | `evalrun`: JSON results of internal eval sets | slice B |
| Run outcomes from task-board: review verdicts, acceptance, rework rounds per role, model and effort | `outcomes`: consumes the feedback records of R12 | after W5 |
| External leaderboards supplied by an operator under their own terms | one importer each; any API key belongs to the host and never enters a record | later |

**No network in importers.** An importer reads files or the module's compiled data and never fetches. Fetching an external source, where it exists, is a separate host command whose output is a file.

---

## 7. Store

- **Root.** The store root is an explicit parameter. W2 decides its default under the Curator data layout and records why.
- **Layout:**
  - `imports/<digest>.json`: immutable;
  - `snapshots/<digest>.json`: immutable;
  - `views/<view-id>.json`: a role-suitability view, keyed by all its inputs (§4);
  - `current`: the digest of the current snapshot, replaced by atomic rename.
- **Concurrency.** Writers take an exclusive (`O_EXCL`) lock beside `current`. Readers need no lock, because every file they read is immutable.
- **Inspection CLI** (`cmr`), each command with `--json`:
  - `cmr evidence import FILE`
  - `cmr evidence ls`
  - `cmr evidence show ID`
  - `cmr evidence unresolved`
  - `cmr note add` / `cmr note retract`
  - `cmr suitability --role ROLE`

---

## 8. Acceptance (workstream W2)

1. **Canonical round-trip.** Import, canonicalise and digest are stable across runs and platforms. Test vectors are committed.
2. **Bug Hunt parity.** The `bughunt` importer reproduces every bench row of the pinned module version, with `evidence_kind` and cost preserved. A table test fails if the module adds a row the importer drops.
3. **Effort exactness.** An observation with an unknown effort never matches a candidate. A transferred value without a rule is refused.
4. **Unresolved models.** An unresolved model is kept, reported, and never matched.
5. **Record lifecycle.**
   - A note past `review_by` weighs zero at the frozen `evaluated_at`.
   - Supersession and retraction are append-only, and the active set does not depend on import order.
   - Test vectors cover: the chain C → B → A; retracting its head (B revives); retracting its middle (A revives); a double supersession (`evidence_conflict`); and a cycle, which refuses its import.
6. **Derivation.** The role-suitability derivation is deterministic and replayable, and its contributions add up to its grade. Test vectors cover a category with no evidence (`unknown`), a notes-only category, a `partial` fingerprint match, and two projects whose requirements differ: their views differ.
7. **Hermetic tests.** Tests use no network and a temporary store root, and run with `-race`.
