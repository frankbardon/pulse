---
name: op-overlay-pairwise-prop-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_PROP_Z
description: Intra-matrix axis-pairwise pooled-SE two-proportion z-test (row-vs-row or col-vs-col within one crosstab).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
---

One host-matrix slot against another ALONG one axis of the SAME crosstab — the per-Request counterpart to Compose's `OVERLAY_PROP_Z_PANEL`. Overlays decorate the host; no `Response.Components` (this family READS them).

## Params

`Scope` (enum, required) — `row` (pair rows per column) or `column` (pair columns per row). `Ref` (object, empty) — intra-matrix — leave empty. Any populated arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.pair_along_dim` (int, unset) — restrict pairs to buckets agreeing on all pair-axis dims but this one. Unset = every pair. `params.n_source` (enum, default `cell_n_unweighted`) — `cell_n_unweighted` / `cell_value_weighted` / `row_margin_n` / `column_margin_n` / `row_margin_distinct` / `column_margin_distinct` / `n_within` / `n_within_distinct` / `cell_weight_sum`. `params.n_within_depth` (int, default `0`) — with `n_source=n_within` or `n_within_distinct`, fixes the first depth+1 pair-axis dims in the denominator (mirrors `CrosstabSpec.NormalizeWithin`); `>=` the pair-axis dim count is refused. `params.p_source` (enum, default `cell_value_pct`) — `cell_value_pct` (0..100, ÷100) or `cell_value` (already 0..1).

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`) + `Response.Components.Crosstab`. Components-disabled host → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX (`Payload.Shape = "matrix"`). PAIR axis = one entry per evaluated `(i, j)` pair, key = the 2-tuple of the legs' labels; OPPOSITE axis echoes the host's other axis. Cell = the pair's two-sided p-value, absent when a leg is unreadable or the test degenerate. `row` scope → rows = pairs; `column` transposes.

## Gotchas

- Reuses `twoProportionZ` — byte-for-byte equal to `OVERLAY_PROP_Z_CELL` / `TEST_PROP_Z` on the same (success, n).
- RAW p-values only — direction, thresholds and min-n flags are the embedder's job; every input is already on the response.
- Degenerate pairs (n=0, pooled ∈ {0,1}, zero SE) fold into one aggregated `PULSE_OVERLAY_REF_ZERO` per reason.
- **`p_source` mismatch fails silently and totally.** `cell_value` over a real 0..100 percentage drives pooled p outside `[0,1]`, so EVERY pair skips and the layer returns empty.
- **`n_within_distinct` counts the slab in distinct KEYS, not records** — same slab as `n_within`, distinct-key cardinality instead of `CellCounts`. Use it when a respondent contributes several records. Admitted ONLY on an `AGG_DISTINCT_SUM` cell (read at `distinct_count`) or `AGG_DISTINCT_COUNT` (read at `cardinality`); every other cell aggregator is refused UP FRONT with `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` naming the observed aggregator. `AGG_FREQUENCY` and `AGG_MODE` spell a key `distinct_count` too, but theirs counts distinct VALUES of the measure field — admission is on aggregator IDENTITY precisely so that never lands as an n.
- **A distinct slab must PARTITION.** A fan-out grouper (`GROUP_SET_PER_ELEMENT`) among the pair-axis dims the slab SUMS ACROSS — depth > `n_within_depth` — is refused with `PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED` at predict AND at runtime, because one key in two summed cells inflates n silently. Inside the fixed prefix (depth <= `n_within_depth`) it is FINE — it multiplies slabs, not cells — as is one on the opposite axis at any depth. Fix by raising `n_within_depth` past the dim, or use `n_within` (record counts are additive).
- **`row_margin_distinct` / `column_margin_distinct` read the MARGIN leg in distinct KEYS** — the cell aggregator's distinct-key figure off `RowMarginComponents[r]` / `ColumnMarginComponents[c]`, same admission as `n_within_distinct`. **Exact by construction and never partition-gated**: a margin accumulates over the raw records that reached the margin key, once each, so a fan-out grouper anywhere cannot double-count it — where summing that row's cells would. Use them when the slab gate refuses and a margin-wide denominator is acceptable. A nil / absent margin entry (components off, or the margin DISPLAY flag off) skips the pair with the aggregated `PULSE_OVERLAY_REF_ZERO` — never a zero n.
- **Null rules, one per admitted aggregator — the n leg counts exactly what the CELL counted.** `AGG_DISTINCT_SUM` registers a key only when the KEY and the VALUE are both non-null; `AGG_DISTINCT_COUNT`'s `cardinality` counts distinct NON-NULL values. A slab cell with no components contributes zero, never a skip.
- Flagged buffered in `OverlayStreamability`, but the HOST crosstab still FUSES on a mergeable cell aggregator (`AGG_WEIGHTED_MEAN`, including over a `GROUP_SET_PER_ELEMENT` axis).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-probit-t`, `op-overlay-prop-z-cell`, `op-test-prop-z`.
