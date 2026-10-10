# Local models: exact weights and their ratings

A plain-language account of how the router rates local models, and why a rating is tied to one exact weights file.
The detailed specification is in [spec/recommend.md](../spec/recommend.md) and the [capability document format](local-capability.md).
Russian version: [local-weights.ru.md](local-weights.ru.md).

> **Implementation status.** This repository ships the offline router and evidence tools. Engine import/load verification, the
> operator-file schema fields shown below, and task-board identity pass-through and run recording are **planned integration work** in
> other components. The router validates supplied identifiers and ratings; it does not verify the loaded file.

## The problem

A local model is a file on disk, for example `qwen-3.8-27b-Q4_K_M.gguf`. Many different files share one name such as "Qwen 3.8 27B":

- different quantizations: Q4 is compressed harder and usually performs worse, while Q8 is more precise;
- different builds and conversions;
- a different chat template or tokenizer.

If models are rated by name, a rating for one file silently applies to another. The router could treat Q4 as being as good as Q8. Or a re-download could put a different file behind the same name, and the run history would not show it.

## The solution: a file fingerprint

Every weights file gets a fingerprint, the SHA-256 hash of the whole file:

```text
gguf-sha256:3f9a0c…(64 hex characters)
```

- The same file always has the same fingerprint, wherever it lives and whatever it is called.
- Changing a single byte gives a different fingerprint.
- The fingerprint covers tokenizer data and any chat template embedded in the GGUF. Runtime overrides, such as an externally supplied
  chat template, do not change the file fingerprint; operators must check their compatibility with the rating separately.

The first version supports only a single, unsplit GGUF file. MLX directories, split GGUF and adapters will get their own prefixes later.

## How it works, step by step

Steps 1–3 are planned engine and configuration integration; steps 4–5 are implemented in the router.

1. **Import (the engine).** When a weights file is added to an engine, the engine computes the fingerprint and records it in its entry in
   `~/.curator/engines.toml`: "engine `local-qwen` serves the file with fingerprint `3f9a…`".
2. **Operator permission (the pin).** In your machine-local `~/.curator/runtimes.toml`, the model row carries `expected_weights_id`. It
   means: "under this name, allow only this file". The pin is mandatory for every local model.
3. **Launch.** The engine opens the file, recomputes its fingerprint and compares three values:
   - the permitted one (the pin);
   - the one recorded at import;
   - the actual one, from the file that was really opened.

   If any of them differ, the launch must be refused with a clear error that lists all three. The engine or client performs this check; a
   router decision on its own does not prove which file was loaded.
4. **Rating (the router).** The router knows how good these weights are in one of two ways:
   - **transfer:** the public score of the base (unquantized) model is multiplied by a quantization coefficient, for example "Q4 is roughly
     this much worse". This is an approximation, and its uncertainty is deliberately widened;
   - **measurement:** a test suite was run on this exact file. For the same quality axis and applicable task scope, an exact-file
     measurement takes precedence over a transfer.

   A rating applies only when **both the file fingerprint and the reasoning mode** match. This version rates only `thinking=on`, with an
   exactly matching, known reasoning effort label. Thinking-off and unknown contexts remain unrated.
5. **No rating, no automatic choice.** If no matching rating exists for a file, the model is unrated, and the router never picks it on its
   own. For a single pipeline, explicitly locking agent, model and effort (including literal `none` where applicable) may return an
   admitted unrated candidate, subject to constraints and hard policy. The decision records its unrated status; execution still requires
   verified weights, pin and reasoning context.

## Your machine-local config

`~/.curator/runtimes.toml` exists on each machine and is not stored in any repository. Each permitted weights file gets **its own model
name** and its own pin. Proposed configuration (the field is not in the schema yet; abbreviated fingerprints are placeholders, not valid
IDs):

```toml
[runtimes.local-qwen.models."qwen-3.8-27b-q8"]
expected_weights_id = "gguf-sha256:3f9a…"   # the Q8 file

[runtimes.local-qwen.models."qwen-3.8-27b-q4"]
expected_weights_id = "gguf-sha256:b71c…"   # the Q4 file
```

Why separate names rather than a list of files under one name:

- Q4 and Q8 have different ratings, and the router must see them as two different options to choose by quality and cost;
- task-board's admission and the run record show directly which file was used;
- adding another variant is just one more row.

If the file bytes change (re-quantized, converted), its fingerprint changes. The operator must supply a new pin and applicable evidence for
the new fingerprint; an old rating never moves to the new file by itself. A one-command re-pin workflow and launch-time enforcement are
planned integration work; this CLI does not provide them. Re-downloading identical bytes leaves the fingerprint unchanged.

## Where ratings come from

- **Base models.** First from public sources that need no key: a leaderboard export saved to a file and loaded with
  `cmr local import-base`. Sources are pluggable, so providers that need a key can be added later. A key never enters records or
  decisions.
- **Quantization coefficients** are operator-supplied (`cmr local coefficients` validates a table). Published llama.cpp KLD/perplexity
  evidence or paired measurements can inform a table, but the operator documents the calibration, uncertainty and expiry for each exact
  target. The library supplies no starting coefficients; applicable exact-file measurements take precedence over transfers.
- **Measurements** record an evidence ID, quality axis, reasoning context, task scope and measured score (0–100 value, standard error,
  source and date). The enclosing materialization identifies the exact weights and quantization and may link a base model. A sample count
  is required for review and optional for other axes. Suite, harness and recipe go in provenance; there are no dedicated fields for them.

This local-weights feature ships no production quantization coefficients or new third-party ratings. Its local capability evidence is
supplied by the operator; the existing embedded catalog includes third-party benchmark ratings for hosted models.

## Who does what

| Part | What it does |
| --- | --- |
| The engine (curator-engines), planned | computes the fingerprint at import, recomputes and checks it at every launch, and reports the actual reasoning mode |
| The `~/.curator` file schema (agents-management), planned | defines where the fingerprint (`engines.toml`) and the pin (`runtimes.toml`) live, and validates their format |
| task-board, planned | passes each local candidate's fingerprint and reasoning mode to the router, and records them on the run |
| The router (this library) | applies a rating only to the exact file in the right mode, and keeps unrated models out of automatic choice |

## What this does not do

- It does not stop the same user from replacing a file on disk. Execution requires a separate engine/client check of the pin, the opened
  file and the actual reasoning context. A router decision does not prove which file was loaded.
- It never infers anything about a model from its name. The name is only a label; the fingerprint tells the truth.
