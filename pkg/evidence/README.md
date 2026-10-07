# Evidence v1

`Import`, `Snapshot`, `Benchmark`, `Observation`, `Note` and `Retraction` follow
`spec/evidence-format.md`. `kind` discriminates import and snapshot documents;
`schema_version` is `evidence-v1`. Required collections are present, including
known-empty arrays. Optional unknown fields are omitted, required unknown strings
use `unknown`, and zero remains a value. Native decoding rejects null, duplicate
keys, extra keys, differently cased keys and missing required fields.

`Prepare` explicitly sorts declared sets: benchmarks by `id@version`, metrics by
name, benchmark categories by category, observations/notes/retractions by address,
category/effort/facet/supersession lists lexically, and note evidence references by
`kind:ref`. Duplicates are refused. `Import.Digest` validates this ordering without
repairing it. Record addresses hash exactly the nested canonical object without
`id`; the temporary encoder envelope does not enter that hash. Snapshot import
sets are lexical. Candidate sets use the lexical canonical full fingerprint as
key. Suitability results preserve that order and never sort by a module rank.

Unknown categories and unresolved models stay in imports and are reported.
Unrecognised efforts stay verbatim and carry `effort_unrecognised` in the report.
Only the frozen model's effort vocabulary can establish an exact effort match.
`Prepare` and `Native` take a frozen registry projection explicitly. They never
register a model. Imports retain source-declared `subject_resolution`; effective
resolution is derived against each snapshot's bound registry. Explicit source
`unresolved` stays unresolved even if the registry knows the model. The
`subject_resolution` stored in an import file is therefore only what the source
declared; `cmr evidence show` and `ls` print the derived value. Unknown
models receive no `effort_unrecognised` issue because their vocabulary is unknown.

The active set is derived from all imports. Withdrawn records have no outgoing
supersession edges; suppression follows paths in the remaining graph. Both active
replacements stay active in a conflict. Retractions cannot target retractions;
unknown future targets are allowed so arrival order does not determine status.
Cycle validation runs before supplied address verification and again over the
complete snapshot during publication. Status and conflicts are inspection output,
never imported fields.

## Store and locking

`NewStore` requires an explicit root. The CLI suggestion is
`XDG_DATA_HOME/curator/model-router/evidence`, with the local share directory as
fallback (`TODO(decision)`). Every command accepts `--store DIR`, and every test
passes `t.TempDir()`.

Imports, snapshots and views are immutable. The additional
`registries/<digest>.json` directory retains the exact vocabulary projection bound
by a snapshot. Snapshot identity includes both the public registry reference and
its digest. Store readers verify content addresses. Views verify the six identity
inputs, embedded derivation/requirements/candidates and contribution arithmetic.

Writers exclusively create `current.lock` with `O_EXCL`. Existing locks return
`evidence_locked`. Locks are never stolen based on an ambient clock or PID.
Following a crash, confirm no writer is running and manually remove the lock.
Immutable files are written and synced to temporary files, then atomically linked
into place. `current` is synced in a temporary file and published by atomic rename.
After renaming `current`, the parent directory is fsynced on a best-effort basis;
platforms or filesystems without directory fsync still retain atomic publication.
Readers need no lock. A failed publication may leave unreferenced immutable files;
it cannot make `current` refer to partly written bytes.

## Bug Hunt interchange

The dependency is pinned to `skill-agents-management v0.5.40`. `PinnedExport`
builds its interchange document in memory from the data-only
`pkg/vendorplugin/benchdata.BugHuntRows()` and `RegistryFacts()` accessors.
`PinnedRegistry`, `BugHunt` and `ImportBugHunt` retain their public APIs; JSON file
imports remain supported. The explicit identity mapping is versioned as
`bughunt-model-map-v1`:

- `schema_version`, `module_version`, `mapping_version`, `imported_at`, `retrieved_at`;
- `models`: `{model_id, efforts[]}` (no-effort axes explicitly use `none`);
- `mapping`: `{external_name, model_id}`, ordered by external name;
- `rows`: `{key, model_name, effort?, kind, value, denominator?, cost?, origin_ref, source_version?}`, ordered by key.

`kind` is `measured`, `interpolated` or `cost`. Measured and interpolated fixed
rows require denominator 105, the declared benchmark scale. Only measured fixed
rows gain `sample_count: 105`. An interpolation retains unknown effort and no
sample count; registry vocabulary never fills observation effort. A cost claim
has no denominator and becomes a `list_cost` observation with
`evidence_kind: measured` and explicit `cost.usd`, agreeing with the value.
Zero cost remains zero; absent cost stays unknown. Cost never contributes a
quality score. Invalid or inconsistent denominators return the typed refusal
`evidence_bughunt_denominator`. Unsupported source versions return
`evidence_bughunt_version`; an omitted source version in JSON uses the pinned
benchmark publication. The accessor's source version is
`bug-hunt-bench@2026-09-13`, independently of the module tag.

Release-declared `imported_at` and `retrieved_at` metadata are fixed at
`2026-10-01T00:00:00Z`. These are import/retrieval metadata, never measurement
times; `observed_at` remains `unknown`. `--imported-at` overrides only the import
time. Reimporting the same pin retains observation addresses and the default
import digest; stores report `already present` for duplicates. Each accessor
export and registry is independent, including nested effort, denominator and
cost data, and concurrent callers share no mutable export state.

`data/bughunt-export.json` is a reviewed JSON interchange vector, not a production
input to `PinnedExport`. The frozen `testdata/bughunt-v0.5.32-export.json` and
canonical import retain the original source baseline. Same-source parity first
compares every fact, registry projection and identity mapping, then restores
only the old `raw_artifact_ref` pin and recomputes observation addresses. The
entire canonical import must equal the baseline. This proves unchanged
observations under the same source identity before updating any new-source
vectors. The `v0.5.40` canonical import and vector manifest separately pin the
export, import, registry, snapshot and every observation address.

The parity invariant imports all 58 accessor claims: 12 fixed/measured,
45 fixed/interpolated and 1 list_cost/measured. It checks both counts and the
complete set of row keys, plus subject, effort, kind, value, denominator/sample
semantics, cost and origin for every claim. Constructor/transcription ownership
belongs to the module; the router no longer exports AST declarations or invokes
`go list` to discover module source. The R1 guard follows the complete evidence
production import closure across build tags and platform suffixes, including
transitive standard-library imports. It refuses vendor implementations, root
`vendorplugin`, `agentic` and `os/exec`; it runs no subprocess. The source guard
uses compiled accessor source locations and requires untrimmed source paths.
Production uses only compiled facts: no vendor registration, availability
checks, harness execution, network retrieval or clock readings.

After same-source parity passes, regenerate and review vectors with the
required wrapper, named packages and offline environment. `$BUILD_WRAPPER` is
the operator-supplied build-lock wrapper of the host (it serializes builds on a
shared machine); any command that runs its arguments works:

```sh
"$BUILD_WRAPPER" env GOENV=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off CMR_UPDATE_EVIDENCE=1 go test -p 1 ./pkg/evidence/... ./cmd/cmr/... -run '^TestBugHuntPinnedVectors$' -count=1
```

Advancing the module pin changes `raw_artifact_ref` and the frozen registry
identity. New observation, import and snapshot addresses are intentional even
when benchmark facts are identical. Stable `origin_ref` preserves publication
deduplication across pins and measured aliases.

**Existing stores need `cmr evidence rebind --registry FILE` for the new
registry**, using a JSON serialization of `PinnedRegistry()` as `FILE`.
`--registry FILE` also supplies another frozen projection for native imports and
local models. `Add` refuses a different projection with
`evidence_registry_mismatch` and names the rebind command. Rebind creates a new
snapshot over the same import digests; earlier snapshots, registry files and
imports remain immutable and replay with their original resolution. Rebind
before importing the new pin; retain old imports for historical replay.

## Derivation and inspection

`DefaultMapping` declares the four playbook roles and fix, implementation, ops,
research, review and routine workloads. Projects supply fully mapped
`RequirementsInput` documents, including their mapping table and facets. V1
weights and thresholds are explicit `TODO(decision)` choices, not admission
rules. Only declared exact benchmark/version/metric normalisations contribute.
A different benchmark or version has no implicit compatibility. An active
observation with no normalisation for its benchmark/metric/category is reported
as `observation_unmapped`, with its observation address and unmapped triple.

V1 normalises Bug Hunt fixed counts to `[0,1]`, discounts partial observations by
0.5, weighs notes by confidence and basis, and averages mapped contributions
within a category. Covered categories are combined by declared positive weights;
a missing category contributes `none`, never a fabricated zero measurement.
Signed contributions sum to relative utility on `[-1,1]`. Strong starts at 0.4,
adequate at 0.2, and weaker supported scores are weak. No supported evidence means
unknown. This is not a calibrated probability.

Known conflicting fingerprint members reject a measurement. Unknown runtime,
harness, engine or engine member, tool or execution profile makes it partial.
Reprints with the same original publication, benchmark version and metric count
once per candidate/category; a direct matching reprint takes precedence over a
partial one, then lexical record address breaks ties. Notes have their own scope:
known efforts, runtime and harness range are direct; unspecified axes are partial.
Harness ranges accept exact versions or inclusive numeric dotted `[a,b]`; other
range syntax remains stored and does not match (`TODO(decision)`). Language,
platform, context-size and horizon requirements refine matching; a scoped role
must match the requested role.

A date-only `review_by` includes that whole UTC date (`TODO(decision)`); RFC3339
expiry is an exact instant. Expired notes remain visible with age and zero weight.
Notes whose `created_at` is later than `evaluated_at` are excluded entirely
from the view, including contributions and coverage; the boundary is inclusive.
This preserves knowledge as of the frozen evaluation time.
Evaluation time is a required library input. Only the CLI may default it to now,
and the resulting view always prints the exact `inputs.evaluated_at` it used.
The view id binds snapshot, derivation, role requirements/mapping, weights/policy,
evaluation time and sorted candidate fingerprints. All inputs needed for replay
are retained in the immutable view.

Example commands, using a store inside the working directory:

```sh
cmr evidence import pkg/evidence/data/bughunt-export.json --importer bughunt --imported-at 2026-10-01T00:00:00Z --store .build/evidence --json
cmr evidence ls --store .build/evidence --json
cmr evidence show RECORD_ADDRESS --store .build/evidence --json
cmr evidence unresolved --store .build/evidence --json
cmr evidence rebind --registry REGISTRY_FILE --store .build/evidence --json
cmr note add --model gpt-6.1-sol --efforts high --categories review.code --statement 'Useful review prior' --author operator --confidence high --review-by 2026-11-01 --at 2026-10-01T00:00:00Z --store .build/evidence --json
cmr note retract RECORD_ADDRESS --reason withdrawn --author operator --at 2026-10-02T00:00:00Z --store .build/evidence --json
cmr suitability --role reviewer --project-reqs REQUIREMENTS_FILE --candidates CANDIDATES_FILE --evaluated-at 2026-10-01T00:00:00Z --store .build/evidence --json
```

`--candidates` accepts a versioned `{schema_version, candidates[]}` document of
public fingerprints. Without it, inspection derives advisory fingerprints from
resolved exact-effort observations and note scopes. A note without an effort
restriction covers the frozen vocabulary; an empty store enumerates registry
model/effort pairs with unknown grades (`TODO(decision)`). These are
never admission decisions or executable offers. `note add --file` accepts a
versioned `{schema_version, note}` document; otherwise flags form a one-note import.
CLI flags may appear before or after positional arguments. JSON refusals go to
stdout with a nonzero exit code; plain refusals go to stderr.

`--store` always overrides the XDG default; the default may move once the Curator
data layout is fixed (`TODO(decision)`). Every command that defaults a time
prints the value in plain and JSON output: import results carry `imported_at`,
Bug Hunt results also carry the pinned `retrieved_at`; note results also carry `created_at` (add) or `at` (retract), and suitability
prints `inputs.evaluated_at`.
