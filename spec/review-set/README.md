# First internal review set

These content-addressed documents are **uncollected templates**, not a real
corpus, runnable protocol, run or model score. Their `.digest` files address
exact canonical JSON bytes. Unknown bindings and an empty case set block
collection. The empty manifest is never used as an evalrun benchmark protocol.

Before collecting, populate the manifest with positive cases and clean controls
from our Go repositories. Each case supplies the declared `case_fields`,
including planted and independently adjudicated defect ids and severity.
Freeze actual revisions, patch digests, allowed context/tools and tuning/holdout
membership. Never use holdout cases to tune profiles. Freeze the manifest digest,
then bind it into the protocol with actual runner, prompts, platform, context,
tools, limits, repeats, dataset revision and split. Publish immutable canonical
artifacts before running; name their exact protocol digest in the benchmark.
After collection starts, changes require a new benchmark version.

F5–F7 defaults are declared in versioned protocol data: planted plus independent
adjudication, human resolution of ambiguity, unweighted F1, separate severity
and critical-defect diagnostics, and one class of Go review on our repositories.
Editing these artifacts and explicitly selecting a new derivation mapping can
change the policy without changing importer code. Diagnostics never enter the
quality mapping. Duplicate findings never earn repeated TP credit.

`ReviewDerivationV2()` retains Bug Hunt and maps only
`internal-review-set@1/review_f1` to `review.code`. The serialized artifact in
`pkg/evidence/testdata/evalrun/review-derivation-v2.json` is suitable for explicit
`cmr suitability --derivation FILE`; the default stays version 1.

Synthetic fixtures under `pkg/evidence/testdata/evalrun/` are test data only.
Their counts do not describe a model or collected corpus. `AggregateReview`
computes the initial objective and omits F1 for incomplete sets or zero
denominators. Clean-review counts remain separate.
