# Execution modes — index

The engine's execution modes: which arm a request takes, the rule that decides it, and what must stay identical across arms. Skills are the agent-facing form; this reference is the contributor view for engine work (`internal/service/`, `internal/processing/`).

**How to load.** This file is an INDEX: the Mode index below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/execution-modes/` your engine work touches. `.claude/reference/execution-modes/history.md` is superseded wording — do not load it by default. A pointer written as `execution-modes.md` (Section) resolves through the section map.

**Rules every mode keeps.** Predict and runtime call the same rule (a shared leaf package or descriptor function), so they accept, refuse and route identically — never write a predict-only or runtime-only twin. A parallel or fused arm must equal the serial / buffered arm (bitwise for sums, matrices and counts; within ULP only for Welford state). A new per-record loop polls ctx. A new slot that names a field extends `processing.NeededFields`, or projected decode silently reads it as null.

## Mode index

One line per mode: identity, knob, named skill; the slices carry the wiring.

- **Streaming Process** (`pulse.ProcessStream`, `--stream`) — four orchestrator modes; the forced-buffered list is `skills/streaming-and-watching.md`.
- **Projected buffered decode** — default-on, output-transparent; `Options{DisableProjection}` / `--no-project`.
- **Parallel Compose** (`pulse.ComposeParallel`, `--parallel N`) — `ComposeOptions{MaxWorkers, PerRequestTimeout, CollectErrors}`; post-slot overlay fold at `internal/service/compose_overlay.go`. `skills/compose-requests.md`.
- **Parallel shards** (`Options.ShardWorkers`) / **parallel buffered Process** (`Options.DecodeWorkers`) — mergeable-only via the engine's internal `CanMergeRequest`, orthogonal to each other; the decode arm needs a mapped single file (`byte-layout.md` (Memory-mapped reads)). `skills/cohort-schema-design.md`.
- **ProcessChain** (`pulse.ProcessChain`) — source-rooted linear chain, mergeable-only, dual-slot overlays. `skills/process-chain.md`.
- **Pushdown hash join** (`Request.Joins`) — exactly one inner join per Request. `skills/join-design.md`.
- **Crosstab / fused crosstab** (`Request.Crosstab`; fusion rule `crosstabfuse.Decide` in `internal/crosstabfuse`, shared by `CanFuseCrosstab` and predict) — composed row × column grid whose margins recompute from raw rows, plus an in-decode streaming arm. `skills/crosstab-guide.md`.
- **Facet endpoints** — simple (`pulse.Facet`) + rich (`pulse.FacetSchema`); the FACET-host overlay kinds ride `FacetRequest.Overlays`. `skills/facet-design.md`.
- **Before-filters attributes** — `ATTR_PREVIOUS_STATE` alone runs over every record before the filter pass; an expression filter may read its label. `execution-modes.md` (Before-filters attributes).
- **Post-window attributes** — a row-local `ATTR_FORMULA` / `ATTR_REG_PREDICT` reading a window label runs after the windows over the record rows; any other window-dependent attribute is `PULSE_ATTRIBUTE_WINDOW_ORDER`. `execution-modes.md` (Post-window attributes).
- **Windows-first** — an aggregation, grouper or crosstab axis reading a window or post-window label runs the windows over the record rows BEFORE the groups / aggregations / crosstab; one classifier, `vectors.WindowsFirst`; buffered, request weight refused. `skills/windows-first.md`; `execution-modes.md` (Windows-first).
- **Filter precompute** (grouped cohorts) — a filter over ONE group's members is evaluated once per dictionary entry, per-row otherwise. `execution-modes.md` (Filter precompute (grouped cohorts)).
- **Grouped tier-1 tests** — `groups` + `tests[]` run every test once per composite group key, buffered; flat `tests[]` with `group_key`. `skills/statistical-testing.md`.
- **Cohort union** (`union` / `union_source_field`; `capability:union`) — several labelled cohorts read as one in-memory shard archive with a virtual categorical source field; every single-cohort surface but a join's right side. `skills/cohort-union.md`; `execution-modes/union.md`.
- **Point lookup** (`pulse.Lookup`) — O(1) key-exact rows via a prebuilt sidecar index; single-file cohorts, equality-only, full-key. `skills/tool-lookup.md`.
- **Cooperative cancellation** — every serial per-record loop polls ctx every 4,096 rows (`processing.CtxPoller`) and returns the ctx error unchanged.
- **Overlays** (`Request.Overlays`, `Response.Overlays`) — additive post-result decorations keyed to host coordinates that **never mutate the base payload**. `skills/overlay-system.md`.
- **Matrices** (`Request.Vectors` + `Request.Matrices`, `Response.Matrices`) — whole-set matrix operators (`MAT_*`) over virtual vectors, folded into per-block `linalg.CoMoment`s through the `BlockMerger` opt-in and emitted at finalize (streaming: terminal flush). `skills/op-mat-covariance.md`; contract `.claude/reference/matrix-and-vectors.md`.
- **Item fan-out** (`for_each` on a window / feature / attribute / aggregation / matrix item) — one item expanded in place over parameter axes before any other check. `skills/item-fan-out.md`.
- **Compose fits** (`ComposedRequest.Fits`, `ComposedResponse.Fits`) — a pre-pass that fits each named one-regression request ONCE before any slot and binds the result into every slot by name. `skills/compose-fits.md`.
- **Scalar-expression slots** (an optimize bound / start; a `WIN_DISTRIBUTED_LAG` / `WIN_EWMA` scalar param; expression-flagged extension params; `AGG_CONSTANT` `expr`) — a number or an expression evaluated ONCE before execution by `internal/scalarslot`; predict lists a model-dependent one under `unresolved_scalars`. `skills/compose-optimize.md`, `skills/op-win-distributed-lag.md`.
- **Result pre-phase** (`result(slot, aggregation)` in an optimize block) — the slots it reads run once after the fits and before the blocks. `skills/compose-optimize.md`.
- **Adaptive sweeps** (an adaptive `sweep.mode`, `budget`) — rounds chosen from the ranked values before, run as barriers inside phase 0. `skills/compose-sweeps.md`.
- **Compose base** (`ComposedRequest.Base`) — Compose sugar: one Request partial merged into every explicit slot before the sweep / banner expansion. `skills/compose-requests.md`.
- **Compose banner** (`ComposedRequest.Banner`, `ComposedResponse.Banner`) — Compose sugar: sections × column groups (+ Total) expanded into crosstab slots and stitched back with per-block letters. `skills/compose-banner.md`.

## Section map

| Section | Slice |
|---|---|
| Streaming Process; Projected buffered decode; Parallel buffered Process; Cooperative cancellation (ctx polling); Request timeout (`Limits.RequestTimeout`) | `execution-modes/streaming-and-decode.md` |
| Compose fits (sweep-bound fits, winning axes, vector / SEM fits, scoring a bound fit); Result pre-phase; Post-fit phase | `execution-modes/compose.md` |
| Categorical-outcome scores (`REG_ORDINAL` / `REG_MULTINOMIAL` columns, the outcome rule, holdout proper scores) | `execution-modes/categorical-scores.md` |
| Adaptive sweeps (strategy registry, budget, rounds inside phase 0, failures, predict); Mode `de`; Mode `bo` | `execution-modes/compose-adaptive.md` |
| Parallel Compose; Compose banner; Compose base; Item fan-out (`for_each`) | `execution-modes/compose-parallel-banner.md` |
| Scalar-expression slots (`internal/scalarslot`, loaded-model fold, window params, constant aggregator, adding a scalar slot) | `execution-modes/scalar-slots.md` |
| Parallel shards (Components parity, incremental merge, group memory, multi-grouper reducers, limits, reproducible sums, tally attributes, attribute slots); Blocked merge (`processing.BlockMerger`) | `execution-modes/shards-and-merge.md` |
| Two-phase `total()` on the parallel reducers and on ProcessChain stages | `execution-modes/parallel-totals.md` |
| Filter precompute (grouped cohorts); Before-filters attributes (`ATTR_PREVIOUS_STATE`); Post-window attributes; Windows-first; Compile-once expressions (request params, exact u64 fields) | `execution-modes/filters-and-expressions.md` |
| ProcessChain; Pushdown hash join (crosstab over a join); Facet endpoints | `execution-modes/chain-join-facet.md` |
| Crosstab (Total column, multi-measure cells, auxiliary margins, `params.fit`, key order, limits, labels); Fused crosstab | `execution-modes/crosstab.md` |
| Cohort union (shape / merge rules, in-memory archive runtime, surfaces, refusals, feature gate, observability) | `execution-modes/union.md` |
| Overlays (streamability, Level / Within, hosts, FORMULA, contributor wiring, adding an overlay kind, weighted hosts, overlay knobs) | `execution-modes/overlays.md` |
| Overlays — COMPOSE host: the COMPOSE arm, Compose series value + key, Compose fan-out, code cells | `execution-modes/overlays-compose.md` |
| Overlays — SERIES host: series measure (`params.field`), series partitions (`params.partition_by`), YoY partitions, SERIES FORMULA, rolling windows, prior-period tests | `execution-modes/overlays-series.md` |
| Overlays — payload parts, `OVERLAY_STD_RESIDUAL`, MATRIX_RESULT host, `OVERLAY_MATRIX_GROUP_SUMMARY`, `OVERLAY_CORRESPONDENCE`, `OVERLAY_RAKE`, spec refusals (one rule per host) | `execution-modes/overlays-matrix-kinds.md` |
| Overlays — paired-flow kinds: `OVERLAY_MARKOV`, `OVERLAY_SYMMETRY`, `OVERLAY_RETENTION` | `execution-modes/overlays-flow-kinds.md` |
| Multiplicity; Grouped tier-1 tests (partitioned post-tests) | `execution-modes/inference.md` |
| Time zones (resolution, call sites, predict and the validators, field references, output namespace); Date-family field types; Labeled date ranges; Smart defaults | `execution-modes/time-and-defaults.md` |
| Per-row zones (`tz_field`, `time_zone_field`); through a join; chain stage ≥ 1 refusal (permanent); cohort default zone field | `execution-modes/per-row-zones.md` |
| Superseded wording, kept for archaeology only | `execution-modes/history.md` (not loaded by default) |

Point lookup has no section here: its contract is `skills/tool-lookup.md` plus `.claude/reference/byte-layout.md`.
