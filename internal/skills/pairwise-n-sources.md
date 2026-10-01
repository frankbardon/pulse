---
name: pairwise-n-sources
description: The sample-size vocabulary shared by every OVERLAY_PAIRWISE_* kind — the nine n_source modes, the three distinct-key modes and their cell-aggregator admission, the slab partition gate, the null rules, and the direct-caller bypass.
type: guide
kind: design
applies_to: process, compose, predict
covers: [OVERLAY, PairwiseOverlayParams, n_source, p_source]
---

# Pairwise sample-size sources

All four `OVERLAY_PAIRWISE_*` kinds decode one shared `OverlaySpec.Params` blob (`types.PairwiseOverlayParams`): `pair_along_dim`, `n_source`, `n_within_depth`, `p_source`. Per-kind math stays in the atomics.

Every mode reads `Response.Components.Crosstab`; a components-disabled host fires `PULSE_OVERLAY_COMPONENTS_REQUIRED` first. An unreadable leg SKIPS the pair (aggregated `PULSE_OVERLAY_REF_ZERO`), never a zero n.

## The nine n_source modes

| `n_source` | Leg |
|---|---|
| `cell_n_unweighted` (default; empty means this) | `CellCounts[r][c]` |
| `cell_value_weighted` | the cell VALUE, truncated |
| `cell_weight_sum` | `CellComponents[r][c]["sum_weights"]` |
| `row_margin_n` / `column_margin_n` | `RowMarginCounts[r]` / `ColumnMarginCounts[c]` |
| `n_within` | the SLAB: sum `CellCounts` across the pair axis, first `n_within_depth`+1 dims held fixed |
| `n_within_distinct` | that slab, in distinct KEYS |
| `row_margin_distinct` / `column_margin_distinct` | `RowMarginComponents[r]` / `ColumnMarginComponents[c]`, in distinct KEYS |

`OVERLAY_PAIRWISE_WELCH_T` and `OVERLAY_PAIRWISE_TWO_MEANS_Z` REFUSE both selectors — every `n_source` and `p_source`, not just the distinct ones. n and both moments come from the Welford triple, so either would be a silent no-op: `PULSE_OVERLAY_PARAM_MISSING` at predict. Predict ONLY: an inert param cannot make a wrong number, so a runtime twin would only break a working `Process`. (A DISTINCT mode is still refused at runtime, under that SAME code.) `n_within_depth` applies to `n_within` / `n_within_distinct` only (`types.PairwiseNSourceUsesWithinDepth`); margin modes ignore it and `>=` the pair-axis dim count is refused.

## Distinct keys versus records

Use a distinct mode when one respondent contributes several records and n must be respondents, not rows. They split on one property: `n_within_distinct` SUMS per-cell cardinalities (`types.PairwiseNSourceSumsDistinctCells`); margin modes read ONE accumulated figure.

## Admission

Distinct modes are admitted on the cell aggregator's IDENTITY, UP FRONT, not per pair, with `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` naming the observed aggregator and the admitted set: `AGG_DISTINCT_SUM` (figure on `distinct_count`) and `AGG_DISTINCT_COUNT` (on `cardinality`).

Everything else is refused, including `AGG_FREQUENCY` and `AGG_MODE`, which BOTH emit a component spelled `distinct_count` — theirs counts distinct VALUES of the measure field (answer codes), not keys. A key-presence probe would read the answer-code count and call it a sample size, so identity is an exact component-key-set match against each aggregator's `ComponentSchema`.

## Null rules

The n leg counts exactly what the CELL counted, and the two admitted aggregators differ: `AGG_DISTINCT_SUM` registers a key only when the KEY and the VALUE are both non-null; `AGG_DISTINCT_COUNT` counts distinct NON-NULL values.

A slab cell no record reached contributes zero: a true zero, not an unreadable leg. A nil or absent MARGIN entry inverts that: components off, or the margin display flag off, means the leg was never emitted, so the pair skips.

## The slab partition rule

Summing per-cell distinct cardinalities equals the slab's true count only when its cells PARTITION the key set. The slab sums across every dim after the fixed prefix, so a fan-out grouper (`GROUP_SET_PER_ELEMENT`) at a summed-across depth lands one key in two cells and n comes out too big. Refused with `PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`.

Two shapes are deliberately ACCEPTED: a fan-out grouper at depth `<= n_within_depth` sits inside the FIXED prefix, multiplying slabs not cells, so each slab still partitions its own keys — the shape the mode exists for; and one on the OPPOSITE axis at any depth, which the slab never sums across. Fix a refusal by raising `n_within_depth` past the offending dim, or switch to `n_within`: record counts ARE additive.

The two MARGIN distinct modes are exact by construction and deliberately NOT gated: a margin accumulates over the raw records that reached the margin key, once each, so there is no per-cell summing to double-count through. Extension groupers are covered too (`GrouperRegistration.FansOut`).

The Compose-host panel raises the SAME code on its own host (`types.CheckPanelSlabPartition`). BOTH its within-prefix modes are gated (`types.PanelNSourceUsesWithinDepth` — there is deliberately no narrower panel `SumsDistinct` predicate), `axis` is always `row`, and Details add `panel_index`/`slot_index`/`slot_label`. It gates its margin leg where this family's `n_within` is ungated, because a panel margin is whatever that slot's cell aggregator emitted — no additivity to claim. See `op-overlay-prop-z-panel`.

## Why it is enforced rather than documented

The failure is silent and liberal: n too large, every p-value too small, nothing in the response says so. So it is a refusal with TWO arms, `descriptor.validateOverlayPairwise` and `processing.applyOverlaysToResponse`, because `pulse.Process` does not run predict. Both call `types.CheckPairwiseSlabPartition`, so message and Details cannot drift.

## Direct-caller bypass

`processing.ApplyOverlaysWithExtensions` is EXPORTED. A caller hand-building a `CrosstabHostView` bypasses BOTH gates: predict never ran, and the runtime twin lives at the response hook that caller skipped, keyed off a pair-axis grouper type the materialised host does not carry. Admission survives but classifies from the host's component key SHAPE, so `AGG_FREQUENCY` under a distinct-bearing shape has its ANSWER-CODE count read as a sample size. Accepted: the exported entry is for embedders who own their host; drive the fold through `pulse.Process` for both gates.

## p_source

Proportion-input kinds only. `cell_value_pct` (default) divides the cell value by 100; `cell_value` takes it as already 0..1. A mismatch fails silently: `cell_value` over a real 0..100 percentage puts every proportion out of range and the layer returns empty.

## The panel shares the vocabulary, not the spellings

The Compose-host `op-overlay-prop-z-panel` reads the same DISTINCT-KEY quantity through the same admission rule above, but NO within-prefix mode name is shared. Its legs: `row_margin_value` (a payload VALUE, not `row_margin_n`), `row_margin_value_within` (that slot's ROW margins summed over a row-key prefix — ALL columns, not `CellCounts` over a pair-axis slab at one fixed opposite index) and `row_margin_distinct_within` (the same slab, read from `RowMarginComponents` as distinct KEYS — a different CARRIER, hence not `..._value_distinct_...`). `row_margin_n`, `n_within` and `n_within_distinct` are all UNKNOWN there and refused, because each would differ from the panel's leg by roughly the column count.

What IS shared: the admitted set (`AGG_DISTINCT_SUM` at `distinct_count`, `AGG_DISTINCT_COUNT` at `cardinality`), exact-identity matching, and the null rules. What the panel ADDS: every slot is judged, and one unadmitted slot — or two slots naming DIFFERENT admitted aggregators — refuses the WHOLE spec. The crosstab arm has one host, so one cell aggregator; a panel pairs across slots and two legs must never be counted in different units.

## See

`overlay-system`, `crosstab-guide`, `op-overlay-pairwise-prop-z`, `op-overlay-prop-z-panel`, `op-agg-distinct-sum`, `op-agg-distinct-count`, `op-group-set-per-element`; example `internal/examples/overlays/42_crosstab_pairwise_distinct_n.json`.
