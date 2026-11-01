# Update Demand — Full Trigger Table

**How to load.** This file is an INDEX. Load it, find the area(s) your change triggers in the table below, then load ONLY those per-area slices under `.claude/reference/update-demand/`. Each slice is self-contained for its triggers (same three columns, same rule). `.claude/reference/update-demand/history.md` is decision history — do NOT load it by default. Unsure which slice holds a row? `grep -l '<identifier>' .claude/reference/update-demand/*.md`.

**Run `sites` first.** Before loading a slice for an operator or slot change, run `make sites ARGS="op <TYPE>"` (existing operator), `make sites ARGS="new-op <category>"` (new operator) or `make sites ARGS="slot <request|response> <name>"` (`go run ./internal/tools/sites …`). It prints the file + declaration checklist derived from code (registries, AST, classified identifier occurrences), the gate tests to rerun, the goldens to regenerate and the slices below to read for that category — `docs/src/internals/finding-change-sites.md`. The slices remain the contract: the tool finds the sites, the slices say what each must contain.

CLAUDE.md carries the compressed one-row-per-category version of this table — enough for the non-skippable gates and skim reading. This index plus its per-area slices under `.claude/reference/update-demand/` is the exhaustive list: every Pulse-internal contract that has its own trigger row.

Rule unchanged: any change in column 1 MUST also update everything in column 2, in the SAME PR. CI fails otherwise.

Defer the doc/skill update to "a follow-up PR" and the follow-up will not happen. Update in the same PR or do not merge.

## Area index

| If your change touches... | Load |
|---|---|
| A registered operator of any category (aggregator, attribute, filterer, grouper, window, feature, `TEST_*`, `REG_*`, synth distribution, `OVERLAY_*` kind), `Group.Include`, operator streamability, `ExactlyOneOf`, an `internal/examples/` request or tag, a skill fence, a built-in Purpose / Interpretation, the intent taxonomy / glossary, the extension guidance hook, advisories, `Recommend` / `Explain`, the default operator table, the regression jackknife knobs, the regression `holdout`, the regression `margins` | `update-demand/operators-and-guidance.md` |
| `pulse.Options.Extensions`, an `extend/` interface, embedder skills / examples, extension matrix operators, extension enum values, `FieldInputsFunc`, label tables (`PULSE_LABEL_TABLES_DIR`) | `update-demand/extensions.md` |
| An error code, an architectural decision, a CLI leaf or flag, the `--json` fatal error envelope, a skill cross-reference, an environment variable, managed-import sidecars, an MCP tool / binding / `mcpserve` entry / resource, feature-profile MCP scope | `update-demand/cli-mcp-errors-env.md` |
| A `--json` envelope or `format_version`, any payload-reachable `types` slot, the undefined-figure (`null`) wire rule, a new non-skippable CI gate, request echo, `MatrixCell.Value`, the public Go surface, `pulse.Limits`, `CanonicalHash`, `StreamResult` / Watch / FilterToFile, manifest operations, `Request.Return` (the `Request.Return` row) | `update-demand/payload-and-return.md` |
| `Response.Components`, `DisableComponents`, grouped aggregator / attribute components, crosstab `margin_aggregations`, a per-operator or extension `ComponentSchema` | `update-demand/components.md` (+ `.claude/reference/response-components.md`) |
| The `.pulse` format or a field type, `FieldWidth`, projection / `DecodePlan` / reusable records, filter precompute, shard archives (layout, set widening, parent groups), `CountRecords`, `cohort inspect` / `pulse_inspect` output, the sidecar point-lookup index and its manifest | `update-demand/format-and-index.md` (+ `.claude/reference/byte-layout.md`) |
| Import: set / float-width inference, null promotion, `SchemaAwareReader`, explicit schemas, `pulse_import` slots, vector folding, total-failure verdict, null-aware readers, source warnings, sidecar emitters | `update-demand/io-import.md` |
| Export / convert / `Materialize` (its `Request.Output` slot included), transport compression, a registered I/O format, `CohortWriter` / `CohortValidator`, the `.sav` writer, SPSS fidelity | `update-demand/io-export.md` |
| `Request.Vectors`, `Request.Matrices` / `Response.Matrices`, a `MAT_*` operator | `update-demand/vectors-and-matrices.md` (+ `.claude/reference/matrix-and-vectors.md`) |
| `ComposedResponse`, sweeps, banners, item `for_each`, ranking, `fits`, model files / `save`, `models`, `populations`, regression `conf_level` / `covariance` | `update-demand/compose.md` |
| The Compose `optimize` block, `ComposedResponse.optimizations`, `internal/optblock`, `capability:compose_optimize` | `update-demand/compose-optimize.md` |
| A `fits` entry's `from_sweep` (sweep-bound fits), `model.CheckFitsIn` / `SweepFitRequest`, `capability:compose_sweep_bind` | `update-demand/compose-sweep-bind.md` |
| `Request.Overlays` / `Response.Overlays`, pairwise / panel params, the Compose overlay fold, overlay error codes, `OverlayLayer.Warnings` | `update-demand/overlays.md` |
| Facet, ProcessChain, joins, crosstab and fusion, `DecodeWorkers`, grouped tier-1 tests, time zones / `internal/temporal`, `Request.Params` | `update-demand/engine-modes.md` (+ `.claude/reference/execution-modes.md`) |
| `Request.Weight` / any per-slot weight (the weighting row), `Pulse.Rake` (the Rake row), `multiplicity` (the Multiplicity row) | `update-demand/weighting-and-multiplicity.md` (+ `.claude/reference/weighting.md`) |
| A new feature or the feature-profile model, request templates, the `observe` vocabulary | `update-demand/features-templates-observe.md` |
| Synth profile document, fidelity report, generation model, type predicates | `update-demand/synth-model.md` (+ `.claude/reference/synthetic-data.md`) |
| Synth structural rules, the pre-claim, rule/spec plumbing | `update-demand/synth-rules.md` (+ `.claude/reference/synthetic-data.md`) |
| Synth rule detection (`--suggest-rules`, co-missing, emission order, exact dependency) | `update-demand/synth-rule-detection.md` (+ `.claude/reference/synthetic-data.md`) |
| Writing or citing a gate: engine / runtime-parity / CI-job gates (Other load-bearing contract gates) and the per-gate prose for CLAUDE.md's list (The CLAUDE.md CI-gate list, per-gate prose) | `update-demand/gates.md` |
| Feature, guidance, effect-size, reference-oracle and generated-docs gates | `update-demand/gates-features-guidance.md` |

Table covers every component category and contract type required by `TestUpdateDemandTableCovers` (case-insensitive checks for: aggregator, attribute, filterer, grouper, error code, format_version, field type, CI gate, environment variable, MCP tool, feature operator). New trigger rows for slot-level contracts go into the matching per-area slice under `.claude/reference/update-demand/` — add a slice (and a row above) when none fits or a slice would pass ~30 KB.

## Section map

Where each heading of the pre-split single file now lives (pointers written as `update-demand.md` (Section) resolve here):

- **The CLAUDE.md table, pre-compression (relocated verbatim at E1-S3)** — every row that is not a verbatim duplicate of a live row sits, unedited, in its area slice under "Category rows (pre-compression CLAUDE.md form)"; the introduction and the verbatim-duplicate rows are in `update-demand/history.md`.
- **Other load-bearing contract gates** — `update-demand/gates.md` (engine, parity, CI jobs) + `update-demand/gates-features-guidance.md` (features, guidance, oracles, generated docs).
- **The CLAUDE.md CI-gate list, per-gate prose (relocated verbatim at U18 E1-S2)** — `update-demand/gates.md`.
- **The `Request.Return` row** — `update-demand/payload-and-return.md`; **the Multiplicity row** and **the weighting row** — `update-demand/weighting-and-multiplicity.md`.
