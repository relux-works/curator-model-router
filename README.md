# curator-model-router

Task-aware model routing for coding agents. Given a description of the work, its role and its constraints, the router chooses a launch configuration (runtime binding + model + reasoning effort) from the candidates that are already admitted and available. It records why in a decision bound to an evidence snapshot and hands the result to the caller's existing spawn gate. The library never admits or launches anything; the CLI can connect its recommendations to task-board spawn. It never switches accounts or egress.

**Status: routing/evidence libraries and task-board CLI integration implemented.**

## Layout

| Path | Content |
| --- | --- |
| `cmd/cmr/` | CLI commands, including recommend, usage and task-board spawn integration |
| `pkg/canonical/` | canonical JSON, typed refusals, SHA-256 digests and golden vectors |
| `pkg/routing/` | frozen shared contracts, usage projection, policy defaults and validation |
| `pkg/recommend/` | pure task-aware catalog selection, hard admission/locks, explanations and replayable decisions (`spec/recommend.md`) |
| `spec/contract-appendix.md` | normative digest contract and vector index |
| `.github/workflows/ci.yml`, `Makefile` | tests, race checks, vet, formatting and actionlint |
| `spec/model-routing.md` | draft specification (decisions R1–R13) |
| `spec/evidence-format.md` | the internal evidence format: benchmarks, observations at an exact effort, internal notes per model, the role-suitability view |
| `spec/headroom.md` | usage facts per `(runtime, managed home)` and the `headroom-aware` strategy |
| `spec/track.md` | delivery track: workstreams W0–W5 and slices A0 to E |
| `spec/model-routing.ru.md`, `spec/evidence-format.ru.md`, `spec/headroom.ru.md`, `spec/track.ru.md` | Russian translations (the English files are canonical) |

## How it fits

- A Go library with caller adapters for task-board and `curator-run`; it creates no processes and imports none of its callers.
- Its CandidateResolver is task-board's agent-selection resolver (`skill-project-management/.specs/drafts/agent-selection.md`): hard filters answer "allowed", "compatible" and "available"; a routing strategy only ranks what passes them.
- The caller launches the chosen candidate through the agents-management launch plane (`skill-agents-management`), and the existing spawn gate re-validates it.
- Usage records per `(runtime, managed home)` are facts of agents-management (`pkg/providerquota`). The router consumes them as frozen snapshot facts and uses them only to order candidates (`spec/headroom.md`).
- Roadmap: slices A0/A1 with milestone M4, slices B–E with milestone M8, in `wiki/roadmap/ecosystem-roadmap.md`.

## Quickstart

```sh
cmr help
cmr recommend --role developer --task-class code.implement
cmr recommend --role developer --task-class code.implement --budget burn
cmr recommend --role developer --task-class code.fix --difficulty hard --json
cmr recommend --role reviewer --task-class review.code --delicate --host "<your-host>"
cmr usage refresh --runtime codex --ttl 10m --json
cmr usage show --json
cmr spawn --task-class code.implement -- TASK --role developer --background
cmr spawn --task-class code.fix --mode recommend --json -- TASK --role developer --background
cmr spawn --task-class research --fanout --budget burn -- TASK --role researcher --background
cmr spawn --task-class review.code --story "<STORY-ID>" --policy ~/.curator/routing.toml -- TASK --role reviewer --background
```

Put the task brief, scope, acceptance criteria, and precondition resources on the board task before spawning. Task-board builds the agent prompt from the role template and that board state; `--instruction RESOURCE` selects a precondition resource.

See [the integration guide](docs/integration.md) for policy defaults, catalog and admission overrides, modes, and usage caching.

Spending policy `budget_mode` defaults to `balanced`. Use `--budget economy` for the cheapest
qualified configuration or `--budget burn` for best quality and up to four distinct A+
families in fan-out. Tier floors and admission still apply. The host review pin is scoped
to normal trivial/routine/standard work; stronger reviews rank quality first and prefer
Codex among the best qualified candidates.

**Real routing data needs an operator overlay.** The embedded catalog is a publishable baseline only: Bug Hunt Bench measurements (MIT) and vendor list prices. Index data from other providers is supplied by each operator in a local overlay file and must never be committed to this repository; a test rejects index values in the embedded catalog.

The public embedded catalog contains pinned Bug Hunt measurements with [attribution](NOTICE) and [field provenance/default picks](catalog/NOTES.md). Overall and coding indices are optional operator inputs. Supply `--catalog-overlay FILE` or policy `catalog_overlay` for an explicit [overlay-v1](spec/recommend.md#11-explicit-operator-overlays) file. Every scalar records its own provenance, and the decision binds the canonical overlay digest for replay. Primary quality ranks above fallback quality; values from different scales are never compared. Unknown quality is never selected.

Module license: [Apache-2.0](LICENSE), copyright Relux Works. Benchmark data retains its upstream terms described in NOTICE.
