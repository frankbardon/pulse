# Execution modes — full prose

CLAUDE.md keeps one-line pointers; this file is the in-depth wiring detail per mode. Skill docs are the canonical longform — this file is the "everything tightly cross-referenced in one place" view for engine work.

**How to load.** This file is an INDEX: the Mode index below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/execution-modes/` your engine work touches. `.claude/reference/execution-modes/history.md` is superseded wording — do NOT load it by default. A pointer written as `execution-modes.md` (Section) resolves through the section map.

## Mode index

Relocated from CLAUDE.md "Execution modes (pointers)" at U16 E1-S2 (CLAUDE.md keeps only the pointer). One line per mode: identity, knob, named skill; the sections below carry the wiring.

- **Streaming Process** (`pulse.ProcessStream`, `--stream`) — four orchestrator modes; the forced-buffered list is `skills/streaming-and-watching.md`.
- **Projected buffered decode** — default-on, output-transparent; `Options{DisableProjection}` / `--no-project`.
- **Parallel Compose** (`pulse.ComposeParallel`, `--parallel N`) — `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}`; post-slot overlay fold at `internal/service/compose_overlay.go`. `skills/compose-requests.md`.
- **Parallel shards** (`Options.ShardWorkers`) / **parallel buffered Process** (`Options.DecodeWorkers`) — mergeable-only via the engine's internal `CanMergeRequest`, orthogonal to each other. `skills/cohort-schema-design.md`.
- **ProcessChain** (`pulse.ProcessChain`) — source-rooted linear chain, mergeable-only at v1, dual-slot overlays. `skills/process-chain.md`.
- **Pushdown hash join** (`Request.Joins`) — v1 is exactly one inner join per Request. `skills/join-design.md`.
- **Crosstab / fused crosstab** (`Request.Crosstab`; fusion rule `crosstabfuse.Decide` in `internal/crosstabfuse`, shared by `CanFuseCrosstab` and predict) — composed row×column grid whose margins recompute from raw rows, plus an in-decode streaming arm. `skills/crosstab-guide.md`.
- **Facet endpoints** — simple (`pulse.Facet`) + rich (`pulse.FacetSchema`); four FACET-host overlay kinds ride `FacetRequest.Overlays`. `skills/facet-design.md`.
- **Before-filters attributes** — `ATTR_PREVIOUS_STATE` alone runs over every record before the filter pass; an expression filter may read its label. `execution-modes.md` (Before-filters attributes).
- **Filter precompute** (grouped cohorts) — a filter over ONE group's members is evaluated once per dictionary entry, per-row otherwise. `execution-modes.md`.
- **Grouped tier-1 tests** — `groups` + `tests[]` run every test once per composite group key, buffered; flat `tests[]` with `group_key`. `skills/statistical-testing.md`.
- **Point lookup** (`pulse.Lookup`) — O(1) key-exact rows via a prebuilt sidecar index; single-file cohorts, equality-only, full-key. `skills/tool-lookup.md`.
- **Cooperative cancellation** — every serial per-record loop polls ctx every 4,096 rows (`processing.CtxPoller`) and returns the ctx error unchanged; section below.
- **Overlays** (`Request.Overlays`, `Response.Overlays`) — additive post-result decorations keyed to host coordinates that **never mutate the base payload**. `skills/overlay-system.md`.
- **Matrices** (`Request.Vectors` + `Request.Matrices`, `Response.Matrices`) — whole-set matrix operators (`MAT_*`) over virtual vectors, folded into per-block `linalg.CoMoment`s through the `BlockMerger` opt-in and emitted at finalize (streaming: terminal flush). `skills/op-mat-covariance.md`; contract `.claude/reference/matrix-and-vectors.md`.
- **Item fan-out** (`for_each` on `Window` / `Feature` / `Attribute`) — one item expanded in place over parameter axes before any other check; section below. `skills/item-fan-out.md`.
- **Compose fits** (`ComposedRequest.Fits`, `ComposedResponse.Fits`) — a pre-pass that fits each named one-regression request ONCE before any slot and binds the result into every slot by name; section below. `skills/compose-fits.md`.
- **Compose banner** (`ComposedRequest.Banner`, `ComposedResponse.Banner`) — Compose sugar: sections × column groups (+ Total) expanded into crosstab slots and stitched back with per-block letters; section below. `skills/compose-banner.md`.

## Section map

| Section (pre-split heading) | Slice |
|---|---|
| Streaming Process; Projected buffered decode; Parallel buffered Process; Cooperative cancellation (ctx polling); Request timeout (`Limits.RequestTimeout`) | `execution-modes/streaming-and-decode.md` |
| Parallel Compose; Compose fits; Compose banner; Item fan-out (`for_each`) | `execution-modes/compose.md` |
| Parallel shards; Blocked merge (`processing.BlockMerger`) | `execution-modes/shards-and-merge.md` |
| Filter precompute (grouped cohorts); Before-filters attributes (`ATTR_PREVIOUS_STATE`); Compile-once expressions (`FILTER_EXPRESSION`, `ATTR_FORMULA`) | `execution-modes/filters-and-expressions.md` |
| ProcessChain; Pushdown hash join; Facet endpoints | `execution-modes/chain-join-facet.md` |
| Crosstab; Fused crosstab | `execution-modes/crosstab.md` |
| Overlays (hosts, Level / Within, per-host wiring, FORMULA, displaced contributor bullets through "Adding an overlay kind", Weighted hosts, Overlay knobs) | `execution-modes/overlays.md` |
| Overlays — SERIES host: series measure (`params.field`), series partitions (`params.partition_by`), YoY partitions, SERIES FORMULA, rolling windows | `execution-modes/overlays-series.md` |
| Overlays — payload parts, Welford migration, `OVERLAY_STD_RESIDUAL`, MATRIX_RESULT host, `OVERLAY_MATRIX_GROUP_SUMMARY`, `OVERLAY_CORRESPONDENCE`, `OVERLAY_MARKOV`, `OVERLAY_SYMMETRY`, `OVERLAY_RETENTION`, `OVERLAY_RAKE`, spec refusals (one rule per host) | `execution-modes/overlays-matrix-kinds.md` |
| Multiplicity; Grouped tier-1 tests | `execution-modes/inference.md` |
| Time zones; Date-family field types; Labeled date ranges; Smart defaults | `execution-modes/time-and-defaults.md` |
| The CLAUDE.md bullets, pre-compression (relocated verbatim at E1-S3) | `execution-modes/history.md` (not loaded by default) |

Point lookup has no section here: its contract is `skills/tool-lookup.md` plus `.claude/reference/byte-layout.md`.
