# Public capability catalog — 2026-10-07

46 canonical configurations, defined by the pinned skill-agents-management v0.5.40 registry and the documented local-model fixture. Admission, account entitlement and runtime availability are caller responsibilities. No harness was executed for this edition.

## Field-source classification

| Fields | Source and disposition |
| --- | --- |
| schema_version | Module wire contract, catalog-v1. |
| provenance | Pinned registry, Bug Hunt CSV commit/digest, public catalog rules. |
| runtime, model, effort, family | Registry declarations; local-qwen is the configured local-model fixture. Muse family identifies the publisher hypothesis, not a verified billing provider. |
| billing | Operator subscription/local classification; no metered rows. |
| constraints | Operator hard constraints, including Muse max-only. |
| quality.overall, quality.coding | Absent in the embedded edition. Operator overlays may supply indices. |
| quality.review.value, kind, source, as_of, stderr, n | Bug Hunt exact harness/model/effort measurements, pinned source and pooled/group SE derivation below. |
| cost.usd_per_task | Mean Bug Hunt live-run list-equivalent cost for 37 measured rows, kind=estimate. Haiku uses an authored small-task estimate; local uses zero incremental API charge. Other costs are unknown. |
| cost.tokens_per_task | Haiku authored assumption only; no benchmark workload token counts are retained. |
| cost.kind, source, as_of | Provenance of the above cost or an explicit operator unknown-cost declaration. |
| latency | Absent; no categorical inference from benchmark timing. |

Artificial Analysis is an optional operator-supplied source under its own terms; this tree contains none of its values or page digests.

## Vendor list prices

These are historical vendor page observations as of 2026-10-04, retained as linked facts. They do not imply current prices, subscription charges or quota units. No task workload is inferred from them. Muse has no retained vendor price because its former price came from an external comparison source.

| Model | Input $/1M | Output $/1M | Cache-read $/1M | Source |
|---|---:|---:|---:|---|
| `claude-fable-5-1` | 10 | 50 | 0.25 | [rate](https://platform.claude.com/docs/en/about-claude/pricing) |
| `claude-haiku-4-5` | 1 | 5 | 0.1 | [rate](https://platform.claude.com/docs/en/about-claude/pricing) |
| `claude-opus-5-5` | 4 | 20 | 0.2 | [rate](https://platform.claude.com/docs/en/about-claude/pricing) |
| `claude-sonnet-5-5` | 2 | 10 | 0.2 | [rate](https://platform.claude.com/docs/en/about-claude/pricing) |
| `gemini-3.5-flash-high` | 1.5 | 9 | 0.15 | [rate](https://ai.google.dev/gemini-api/docs/pricing) |
| `gemini-3.5-flash-low` | 1.5 | 9 | 0.15 | [rate](https://ai.google.dev/gemini-api/docs/pricing) |
| `gemini-3.5-flash-medium` | 1.5 | 9 | 0.15 | [rate](https://ai.google.dev/gemini-api/docs/pricing) |
| `gemini-3.6-flash-high` | 0.75 | 3.75 | 0.075 | [rate](https://ai.google.dev/gemini-api/docs/pricing) |
| `gemini-3.6-flash-low` | 0.75 | 3.75 | 0.075 | [rate](https://ai.google.dev/gemini-api/docs/pricing) |
| `gemini-3.6-flash-medium` | 0.75 | 3.75 | 0.075 | [rate](https://ai.google.dev/gemini-api/docs/pricing) |
| `gemini-3.7-flash-high` | 0.75 | 3.75 | 0.075 | [rate](https://ai.google.dev/gemini-api/docs/pricing) |
| `gemini-3.8-flash-high` | 0.75 | 3.75 | 0.075 | [rate](https://ai.google.dev/gemini-api/docs/pricing) |
| `gpt-6-astra` | 10 | 50 | 1 | [rate](https://developers.openai.com/api/docs/models/gpt-6-astra) |
| `gpt-6-luna` | 0.1 | 0.5 | 0.01 | [rate](https://developers.openai.com/api/docs/models/gpt-6-luna) |
| `gpt-6-sol` | 2 | 10 | 0.2 | [rate](https://developers.openai.com/api/docs/models/gpt-6-sol) |
| `gpt-6.1-sol` | 2 | 10 | 0.1 | [rate](https://developers.openai.com/api/docs/models/gpt-6.1-sol) |

Haiku's independent authored assumption is 100000 input tokens and 8000 output tokens, without cache: (100000×1 + 8000×5)/1e6 = $0.14. This is not telemetry. Local zero excludes hardware, electricity and contention. Bug Hunt cost is an estimate for its two-repository workload, reused as a resource proxy for selection across classes; it is not a measured cost of an arbitrary project task. Unknown costs remain absent rather than zero.

## Quality sources, tier edges and ranking

code.*, tool-use and ops use coding as primary; review.* uses review; all other classes use overall. If the primary is absent, the Bug Hunt review measurement is the fallback. A missing review measurement leaves review quality unknown. There is no substitution between coding and overall indices.

Primary candidates rank above fallback-only candidates after hard admission, billing and tier filters, in every budget and objective. Only values within one scale are compared; headroom and preferences cannot cross source groups. Explanations identify quality_index and quality_from_bughunt_fallback. Unknown quality is never selected.

Index edges in tier_edges: S ≥62, A ≥56, B ≥45. Measured edges in review_tier_edges: S ≥40, A ≥30, B ≥20, used for review and every Bug Hunt fallback. Both are operator-overridable. Tiers use value minus one stderr when supplied, otherwise the point value. These are routing bands, not validated project success thresholds. Review noise ties remain anchored on the largest remaining mean, then cost/key; no chained tie bands.

## Bug Hunt attribution and measurement method

Bug Hunt Bench by Pawel Huryn declares MIT reuse in its README. Link back: [repository](https://github.com/phuryn/bug-hunt-bench), [live benchmark](https://bughunt.productcompass.pm), [README citation](https://github.com/phuryn/bug-hunt-bench#how-do-i-cite-this). See also NOTICE.

Suggested citation from the README:

> Huryn, P. *Bug Hunt Bench: 105 real bugs, two production repos, frontier coding models graded blind.* https://bughunt.productcompass.pm — data: https://github.com/phuryn/bug-hunt-bench

**Measured review proxy — evidence refresh.** [Pinned runs.csv](https://raw.githubusercontent.com/phuryn/bug-hunt-bench/1217192a6d04e89da3f6106ca3a304d2734882eb/results/runs.csv), fetched 2026-10-05, commit `1217192a6d04e89da3f6106ca3a304d2734882eb`. The local importer in [tools/review-import](../tools/review-import/README.md) verifies CSV SHA-256 `995ee322bab1019790fb58cc6c25af968e688f2952d8e28380e975f222407a74`. It maps 37 exact catalog configurations, leaving 9 without a review value. Review values use `kind=measured`, the pinned repo@commit source, latest contributing live-run date as `as_of`, `stderr` and `n`.

`mean = fixed_of_105 / 105 × 100`. For n≥2, SE is each group's sample SD/√n. Singletons borrow the pooled within-configuration SD across all repeated live groups: 3.285552612 fixed points, 81 residual degrees of freedom, then normalize by 100/105. This assumes homogeneous variance. The table's ± is one SE, not the research report's pooled 95% half-width. Repeated runs reuse the same two TypeScript repositories and 105 bugs; their variation does not establish generalization to new repositories or independent-case uncertainty. Bug Hunt measures judge-graded finding and fixing of planted bugs: it is a review proxy, not Go-review, spec-review or adjudicated finding accuracy. Judge identity/family effects require further evidence. The Muse contributor runtime has no review value because its execution binding to the public Meta API harness is unverified.

| Configuration | Mean /100 | n | ± SE /100 | Wall minutes | List-equiv. cost USD |
|---|---:|---:|---:|---:|---:|
| `agy/gemini-3.7-flash-high/none` | 15.238 | 1 | 3.129 | 22.70 | $6.36 |
| `agy/gemini-3.8-flash-high/none` | 17.143 | 3 | 1.455 | 38.73 | $11.02 |
| `codex/gpt-6-luna/low` | 3.810 | 1 | 3.129 | 19.30 | $0.20 |
| `codex/gpt-6-luna/max` | 17.460 | 3 | 1.270 | 99.17 | $0.53 |
| `codex/gpt-6-luna/high` | 8.571 | 1 | 3.129 | 24.40 | $0.13 |
| `codex/gpt-6-luna/xhigh` | 13.333 | 1 | 3.129 | 57.00 | $0.39 |
| `codex/gpt-6-luna/medium` | 3.810 | 1 | 3.129 | 8.60 | $0.06 |
| `codex/gpt-6-astra/low` | 25.714 | 1 | 3.129 | 32.10 | $11.69 |
| `codex/gpt-6-astra/max` | 42.857 | 3 | 2.397 | 89.80 | $33.03 |
| `codex/gpt-6-astra/high` | 33.333 | 1 | 3.129 | 40.50 | $20.60 |
| `codex/gpt-6-astra/xhigh` | 40.952 | 1 | 3.129 | 58.80 | $24.22 |
| `codex/gpt-6-astra/medium` | 32.381 | 1 | 3.129 | 28.00 | $15.78 |
| `codex/gpt-6.1-sol/low` | 21.429 | 2 | 8.095 | 25.30 | $1.33 |
| `codex/gpt-6.1-sol/max` | 42.222 | 3 | 1.384 | 139.33 | $5.88 |
| `codex/gpt-6.1-sol/high` | 34.762 | 2 | 1.429 | 70.30 | $3.26 |
| `codex/gpt-6.1-sol/xhigh` | 40.635 | 3 | 0.840 | 98.73 | $4.35 |
| `codex/gpt-6.1-sol/medium` | 27.619 | 2 | 0.952 | 29.80 | $1.76 |
| `codex/gpt-6-sol/low` | 5.714 | 1 | 3.129 | 11.10 | $1.08 |
| `codex/gpt-6-sol/max` | 27.937 | 3 | 1.680 | 63.40 | $9.34 |
| `codex/gpt-6-sol/high` | 19.048 | 1 | 3.129 | 33.90 | $4.16 |
| `codex/gpt-6-sol/xhigh` | 23.810 | 1 | 3.129 | 51.40 | $7.67 |
| `codex/gpt-6-sol/medium` | 13.333 | 1 | 3.129 | 24.50 | $2.39 |
| `claude/claude-opus-5-5/low` | 21.270 | 3 | 1.680 | 11.53 | $8.34 |
| `claude/claude-opus-5-5/max` | 39.683 | 3 | 1.270 | 66.90 | $58.52 |
| `claude/claude-opus-5-5/high` | 30.159 | 3 | 0.317 | 23.97 | $22.24 |
| `claude/claude-opus-5-5/xhigh` | 34.286 | 3 | 0.550 | 42.57 | $34.98 |
| `claude/claude-opus-5-5/medium` | 28.889 | 3 | 0.317 | 17.03 | $15.69 |
| `claude/claude-fable-5-1/low` | 27.619 | 1 | 3.129 | 33.20 | $33.00 |
| `claude/claude-fable-5-1/max` | 40.952 | 1 | 3.129 | 73.10 | $87.18 |
| `claude/claude-fable-5-1/high` | 31.429 | 1 | 3.129 | 36.30 | $48.58 |
| `claude/claude-fable-5-1/xhigh` | 27.619 | 1 | 3.129 | 59.50 | $53.61 |
| `claude/claude-fable-5-1/medium` | 20.000 | 1 | 3.129 | 23.00 | $23.73 |
| `claude/claude-sonnet-5-5/low` | 18.095 | 3 | 0.952 | 8.50 | $5.73 |
| `claude/claude-sonnet-5-5/max` | 48.889 | 3 | 4.053 | 287.17 | $154.04 |
| `claude/claude-sonnet-5-5/high` | 32.698 | 3 | 1.384 | 27.97 | $19.85 |
| `claude/claude-sonnet-5-5/xhigh` | 34.286 | 3 | 1.455 | 72.70 | $42.86 |
| `claude/claude-sonnet-5-5/medium` | 22.222 | 3 | 2.768 | 17.40 | $11.12 |

Nine rows have no exact mapped measurement: Muse contributor binding is unverified, hosted Qwen cannot establish local-engine quality, and the remaining configurations have no applicable live runs. No quality is inferred for these rows.

## Default picks without an overlay

The table below explicitly admits all embedded rows, uses built-in policy with no standing rules, locks, usage facts or producer family, and normal sensitivity with a single pipeline. Roles map to developer/code.implement, reviewer/review.code, researcher/research and orchestrator/orchestration. These examples do not assert live availability. Each cell includes configuration and tier.

<!-- DEFAULT-PICKS -->
| Role / class | Difficulty | Economy | Balanced | Burn |
| --- | --- | --- | --- | --- |
| developer / code.implement | trivial | `codex/gpt-6-luna/medium` (C) | `codex/gpt-6-luna/medium` (C) | `claude/claude-sonnet-5-5/max` (S) |
| developer / code.implement | routine | `codex/gpt-6.1-sol/medium` (B) | `codex/gpt-6.1-sol/medium` (B) | `claude/claude-sonnet-5-5/max` (S) |
| developer / code.implement | standard | `codex/gpt-6.1-sol/medium` (B) | `codex/gpt-6.1-sol/medium` (B) | `claude/claude-sonnet-5-5/max` (S) |
| developer / code.implement | hard | `codex/gpt-6.1-sol/high` (A) | `claude/claude-sonnet-5-5/max` (S) | `claude/claude-sonnet-5-5/max` (S) |
| developer / code.implement | critical | `codex/gpt-6.1-sol/max` (S) | `claude/claude-sonnet-5-5/max` (S) | `claude/claude-sonnet-5-5/max` (S) |
| reviewer / review.code | trivial | `codex/gpt-6-luna/medium` (C) | `codex/gpt-6-luna/medium` (C) | `claude/claude-sonnet-5-5/max` (S) |
| reviewer / review.code | routine | `codex/gpt-6.1-sol/medium` (B) | `codex/gpt-6.1-sol/medium` (B) | `claude/claude-sonnet-5-5/max` (S) |
| reviewer / review.code | standard | `codex/gpt-6.1-sol/medium` (B) | `codex/gpt-6.1-sol/medium` (B) | `claude/claude-sonnet-5-5/max` (S) |
| reviewer / review.code | hard | `codex/gpt-6.1-sol/high` (A) | `claude/claude-sonnet-5-5/max` (S) | `claude/claude-sonnet-5-5/max` (S) |
| reviewer / review.code | critical | `codex/gpt-6.1-sol/max` (S) | `claude/claude-sonnet-5-5/max` (S) | `claude/claude-sonnet-5-5/max` (S) |
| researcher / research | trivial | `codex/gpt-6-luna/medium` (C) | `codex/gpt-6-luna/medium` (C) | `claude/claude-sonnet-5-5/max` (S) |
| researcher / research | routine | `codex/gpt-6.1-sol/medium` (B) | `codex/gpt-6.1-sol/medium` (B) | `claude/claude-sonnet-5-5/max` (S) |
| researcher / research | standard | `codex/gpt-6.1-sol/medium` (B) | `codex/gpt-6.1-sol/medium` (B) | `claude/claude-sonnet-5-5/max` (S) |
| researcher / research | hard | `codex/gpt-6.1-sol/high` (A) | `claude/claude-sonnet-5-5/max` (S) | `claude/claude-sonnet-5-5/max` (S) |
| researcher / research | critical | `codex/gpt-6.1-sol/max` (S) | `claude/claude-sonnet-5-5/max` (S) | `claude/claude-sonnet-5-5/max` (S) |
| orchestrator / orchestration | trivial | `codex/gpt-6-luna/medium` (C) | `codex/gpt-6-luna/medium` (C) | `claude/claude-sonnet-5-5/max` (S) |
| orchestrator / orchestration | routine | `codex/gpt-6.1-sol/medium` (B) | `codex/gpt-6.1-sol/medium` (B) | `claude/claude-sonnet-5-5/max` (S) |
| orchestrator / orchestration | standard | `codex/gpt-6.1-sol/medium` (B) | `codex/gpt-6.1-sol/medium` (B) | `claude/claude-sonnet-5-5/max` (S) |
| orchestrator / orchestration | hard | `codex/gpt-6.1-sol/high` (A) | `claude/claude-sonnet-5-5/max` (S) | `claude/claude-sonnet-5-5/max` (S) |
| orchestrator / orchestration | critical | `codex/gpt-6.1-sol/max` (S) | `claude/claude-sonnet-5-5/max` (S) | `claude/claude-sonnet-5-5/max` (S) |
