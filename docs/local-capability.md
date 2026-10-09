# Offline local GGUF ratings

Supply independently pinned whole-file GGUF identity and actual thinking context
for each admitted local candidate. Admission still comes from the caller. The
router does not hash/load model files, infer lineage from aliases, or verify
engine observations. This slice rates thinking only, matching benchmark effort
labels exactly. Execution needs the engine/client's fresh pin and load checks.

Local evidence is operator data. The public embedded catalog ships no new
leaderboard scores or quantization discounts. The `fictional-*` files under
`pkg/recommend/testdata` contain synthetic arithmetic and synthetic provenance;
they are not ratings for any real model or executable GGUFs.

1. Export keyless public benchmark results into `public-base-export-v1`. Preserve
   canonical HF model spelling, checkpoint revision or explicit unknown,
   source-specific rights/attribution, original metric and reasoning context.
   Supply a reviewed per-axis calibration to 0–100 and declared uncertainty.
   Use `fictional-public-base-export.json` as a shape reference. Import with:

   ```sh
   cmr local providers
   cmr local import-base --provider public-json --input public-export.json > base-records.json
   ```

   The importer stamps its ID/version and exact source-file SHA-256. It does not
   fetch a leaderboard or authenticate. Missing calibration/uncertainty refuses.
   Additional providers implement `recommend.BaseScoreProvider` and register in
   an explicit registry; their metadata includes whether a key is required.
   Keys belong in private provider configuration, never in exported records.

2. Build a `quantization-coefficients-v1` operator table from reviewed published
   KLD/perplexity evidence or paired measurements. Bind every record to exact W,
   the imported base-record ID, quantization annotation, axis, thinking context
   and scope. Published KLD/PPL is transfer evidence, not automatically an agentic
   quality coefficient. Record the calibration rationale, wide retention bounds,
   additional uncertainty and UTC expiry. Review scores require exact local
   measurements. The library supplies no coefficient numbers. Validate with:

   ```sh
   cmr local coefficients --input coefficients.json > validated-coefficients.json
   ```

3. Assemble `local-capability-v1` from the imported base records, exact
   materializations and validated coefficient table. Add applicable exact-W
   measurements when available, and an ordered evidence ID `source_priority`
   for ties within a precedence class. Measurement-only targets may omit their
   base link. Keep recipe/template and benchmark applicability in provenance
   and review scope before reuse. Follow `fictional-local-capability.json` for
   required closed shapes.

4. Pass explicit admitted candidates with `weights_id`, mandatory matching
   `expected_weights_id`, and `reasoning`. A profile-fixed tuple may use
   `effort=none` while reasoning records `thinking=on, effort=xhigh`.
   Candidate IDs use exactly `gguf-sha256:` plus 64 lowercase hex digits.
   Supply local catalog rows with `billing=local` and empty inline quality;
   optional `weights_id`, `expected_weights_id`, and reasoning row guards
   further restrict matching. A row pin must equal the admitted W, even on a
   pin-only row. Conflicts remain UNRATED with a mismatch explanation. Merging
   preserves supplied row guards and fills only absent fields from admission;
   a row guard never supplies missing admission identity, pin or context.

   ```sh
   cmr recommend --catalog catalog.json --candidates candidates.json --local-capability capability.json --role developer --task-class code.implement --json
   ```

   For fully offline selection use `cmr local decide --input request.json`.
   The v2 request contains the ordinary catalog/policy/task/admission fields,
   positive frozen `usage.as_of`, and `local_capability={id,json}`. Construct this
   byte-preserving bundle with `recommend.FreezeLocalCapability`; its json string
   must retain the exact bytes hashed by id. The offline command writes only its
   decision to stdout and does not save to a state directory.
   JSON file input must contain arrays for `usage.facts`, `usage.inflight` and
   `usage.scope_map.entries`, using `[]` when empty. Nulls are rejected before
   normalization.

The transfer explanation includes unpenalized quality, declared uncertainty and
conservative selection quality. Exact applicable measurements always win, even
when lower. Missing/mismatched identity, missing pins, incompatible reasoning,
expired transfers and missing axis scores stay UNRATED. Fully explicit tuple
locks may return an advisory unrated selection with reasons; it cannot enter
fan-out or override admission/constraints/policy. Such a selection never
establishes permission to execute unverified weights.

Evidence scopes must be `*` or one of the router's supported task classes.
Unknown classes and misspellings reject the document during loading or import.
Transfer tier and quality comparisons use exact decimal rationals; displayed
quality values retain the ordinary catalog representation.

Hosted overlay-v1 loading remains available. New local rows cannot be patched
through it, even for cost only. Store local documents privately according to
source rights. Frozen v2 decisions include their exact evidence bytes and replay
without external reads. The normative contract is [spec/recommend.md §9](../spec/recommend.md#9-exact-local-gguf-capability).
