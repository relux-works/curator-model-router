# Import measured review quality

`review-import` reads an explicit **local** copy of the public Bug Hunt `runs.csv`.
It performs no network, account, harness, policy-home or usage-cache operations.
Run from the repository root, through the build wrapper required by your environment:

```sh
mkdir -p .temp
curl --fail --location https://raw.githubusercontent.com/phuryn/bug-hunt-bench/1217192a6d04e89da3f6106ca3a304d2734882eb/results/runs.csv -o .temp/runs.csv
# Prefix Go commands with your build wrapper when required.
GOFLAGS=-mod=mod GOPROXY=off go run ./tools/review-import \
  --csv .temp/runs.csv --catalog catalog/catalog.json \
  --catalog-out catalog/catalog.json > .temp/review-report.json
```

Generate the notes table with the same command and `--format markdown` (unmapped groups are printed to stderr).

The tool verifies the pinned CSV SHA-256 before writing a catalog. It is tied to this pinned source and the compiled `skill-agents-management`
registry at v0.5.40; a different snapshot requires updating the source pin and hash. The report includes the input SHA-256, live run IDs, pooled residual degrees
of freedom, all matched configurations and every unmatched group with a reason.
`as_of` is the latest live run date in each configuration, not the import date.
Re-running on the same input produces the same catalog and report. Tests use the
small checked-in synthetic CSV; they never fetch benchmark data.

Each configuration groups exact `(harness, model, effort)`, keeps only
`row_status=live`, and calculates `mean(fixed_of_105) * 100 / 105`. For `n >= 2`,
`stderr = sample_SD / sqrt(n) * 100 / 105`. A singleton uses the pooled within-group
SD across **all** repeated live configurations, including unmatched ones:
`sqrt(sum_g sum_r (score_r - mean_g)^2 / sum_g (n_g - 1))`. For this pinned CSV,
that is 3.285552612 raw fixed points, with 81 residual degrees of freedom. No
pooled support for a matched singleton is an error. This assumes homogeneous run
variance and measures variation on the same two repositories, not generalization
to new projects. `±` in the generated table is one SE, not a 95% confidence band.

Public display names and harnesses have explicit mappings, validated against
registry model/effort vocabulary and exact catalog membership. Antigravity's
Gemini effort is part of its model ID (`gemini-3.8-flash-high`, effort `none`).
Retired Gemini CLI results never enter that row. Hosted Qwen never enters a local
engine row. All `Muse Code / Meta API` groups remain unmapped with
`harness_binding_unverified`: no evidence establishes that this harness exercised
the catalog's Muse contributor runtime. Its review value remains absent.
Missing per-run cost remains unknown; unmatched groups still contribute their scores to pooled variance. A mapped group must have a published per-run cost and `cost_kind=list`; bill, free and floor costs are never
relabelled list-equivalent. The tool emits mean wall minutes and mean published
list cost separately. It changes only `quality.review`: existing catalog costs,
latency and other quality indices retain their own provenance and meaning.

The optional catalog-v1 `quality.review` carries `value`, `kind=measured`, pinned
`source`, `as_of`, `stderr` and positive integer `n`. A source refresh clears
obsolete review values from this source before applying the newly matched rows.
Other review sources and every non-review field are preserved. Unmeasured
configurations receive no review value. A registry-valid configuration outside
the catalog appears in `unmapped` as `catalog_configuration_absent`.

Bug Hunt finds and fixes planted bugs in TypeScript using a model judge. Its score
is a **review proxy**, not a measured code-review finding rate or Go/spec review
accuracy. Language, task transfer, judge identity and judge-family bias still need
project-specific evidence; no local benchmark or inference was performed.
