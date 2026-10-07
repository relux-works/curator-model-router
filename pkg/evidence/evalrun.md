# Internal eval exports

`EvalRunImport(bytes, mapping, registry)` and `ImportEvalRun(value, mapping,
registry)` create immutable evidence using only supplied facts. Validate mapping
JSON with `Decode` into `EvalRunMapping`; `Digest()` returns its canonical
identity. Source exports must bind that identity and exact name/version.
`Store.AddEvalRun` merges the importer report with store/lifecycle diagnostics;
callers composing `ImportEvalRun` with `Store.Add` should use `MergeReports`.

```
cmr evidence import-evalrun export.json --mapping mapping.json --registry registry.json --store evidence --json
cmr evidence import export.json --importer evalrun --mapping mapping.json --registry registry.json --store evidence --json
cmr suitability --role reviewer --derivation derivation.json --evaluated-at 2026-10-02T00:00:00Z --store evidence --json
```

The export supplies import/retrieval time. `--imported-at` is refused for
evalrun, including an explicitly empty value. Other importers refuse
`--mapping`. No raw artifact is opened. Exact source names are mapped without
case folding; unmapped models and stated unmapped runtimes remain unresolved.
An absent runtime is unknown and permits partial matching. Effort is never
inferred. Completed results require positive independent case counts; runners
omit incomplete/cancelled/missing aggregates and report diagnostics separately.
Costs are copied as per-sample values, with no denominator conversion.

`ReviewDerivationV2()` is opt-in. It adds only the version 1 internal review F1
normalisation; diagnostic counts are inspectable as `observation_unmapped`.
The protocol/manifest templates in `spec/review-set/` describe collection gates
and the pending F5–F7 defaults without inventing a real dataset or measurement.
The CLI continues to use derivation 1 unless `--derivation FILE` is supplied.
