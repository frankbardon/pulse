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

`Scope` (enum, required) — `row` (pair rows per column) or `column` (pair columns per row). `Ref` (object, empty) — intra-matrix — leave empty. Any populated arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.pair_along_dim` (int, unset) — restrict pairs to buckets agreeing on all pair-axis dims but this one. Unset = every pair. `params.n_source` (enum, default `cell_n_unweighted`) — `cell_n_unweighted` / `cell_value_weighted` / `row_margin_n` / `column_margin_n` / `n_within` / `cell_weight_sum`. `params.n_within_depth` (int, default `0`) — with `n_source=n_within`, fixes the first depth+1 pair-axis dims in the denominator (mirrors `CrosstabSpec.NormalizeWithin`). `params.p_source` (enum, default `cell_value_pct`) — `cell_value_pct` (0..100, ÷100) or `cell_value` (already 0..1).

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`) + `Response.Components.Crosstab`. Components-disabled host → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX (`Payload.Shape = "matrix"`). PAIR axis = one entry per evaluated `(i, j)` pair, key = the 2-tuple of the legs' labels; OPPOSITE axis echoes the host's other axis. Cell = the pair's two-sided p-value, absent when a leg is unreadable or the test degenerate. `row` scope → rows = pairs; `column` transposes.

## Gotchas

- Reuses `twoProportionZ` — byte-for-byte equal to `OVERLAY_PROP_Z_CELL` / `TEST_PROP_Z` on the same (success, n).
- RAW p-values only — direction, thresholds and min-n flags are the embedder's job; every input is already on the response.
- Degenerate pairs (n=0, pooled ∈ {0,1}, zero SE) fold into one aggregated `PULSE_OVERLAY_REF_ZERO` per reason.
- **`p_source` mismatch fails silently and totally.** `cell_value` over a real 0..100 percentage drives pooled p outside `[0,1]`, so EVERY pair skips and the layer returns empty.
- Flagged buffered in `OverlayStreamability`, but the HOST crosstab still FUSES on a mergeable cell aggregator (`AGG_WEIGHTED_MEAN`, including over a `GROUP_SET_PER_ELEMENT` axis).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-probit-t`, `op-overlay-prop-z-cell`, `op-test-prop-z`.
