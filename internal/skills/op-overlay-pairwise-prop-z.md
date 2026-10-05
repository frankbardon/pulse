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

One host-matrix slot against another ALONG one axis of the SAME crosstab — the per-Request counterpart to Compose's proportion-z panel. Overlays decorate the host; no `Response.Components` (this family READS them).

## Params

`Scope` (enum, required) — `row` (pair rows per column) or `column` (pair columns per row). `Ref` (object, empty) — intra-matrix; any populated arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.pair_along_dim` (int, unset) — restrict pairs to buckets agreeing on all pair-axis dims but this one; unset = every pair. `params.n_source` (default `cell_n_unweighted`), `params.n_within_depth` (default `0`) and `params.p_source` (default `cell_value_pct`) are the family-wide vocabulary — nine sample-size modes, three of them distinct-KEY: `pairwise-n-sources`.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched (`multiplicity-correction`).
<!-- /feature -->

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`) + `Response.Components.Crosstab`. Components-disabled host → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX (`Payload.Shape = "matrix"`). PAIR axis = one entry per evaluated `(i, j)` pair, key = the 2-tuple of the legs' labels; OPPOSITE axis echoes the host's other axis. Cell = the pair's two-sided p-value, absent when a leg is unreadable or the test degenerate. `row` scope → rows = pairs; `column` transposes.

## Gotchas

- Weighted host (both kinds, any source): p̂ = the weighted cell share (Σw_success/Σw_base); with `n_source` omitted n = the cell's N* (`sum_weights` frequency, `n_eff` probability). Unweighted-count modes (and `cell_weight_sum` / `cell_value_weighted` under probability) are `PROCESSING_CONFIG` in predict AND runtime (`pairwise-n-sources`). `Summary.Parameters` adds `sum_weights` (+ `n_eff`).
- Reuses `twoProportionZ` — byte-for-byte equal to every two-proportion z test and overlay on the same (success, n).
- RAW p-values only — direction, thresholds and min-n flags are the embedder's job.
- Degenerate pairs (n=0, pooled ∈ {0,1}, zero SE) fold into one aggregated `PULSE_OVERLAY_REF_ZERO` per reason.
- **`p_source` mismatch fails silently and totally.** `cell_value` over a real 0..100 percentage drives pooled p outside `[0,1]`, so EVERY pair skips and the layer returns empty.
- **The three distinct-KEY n modes are admitted only on a distinct-key cell aggregator** (sum-of-distinct or distinct count) — the modal aggregators spell a `distinct_count` too, but theirs counts answer codes. `n_within_distinct` additionally refuses a fan-out grouper at a summed-across depth (`PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`, predict AND runtime); the margin modes are exact by construction and never gated. Null rules and the direct-caller bypass: `pairwise-n-sources`.
- Flagged buffered in `OverlayStreamability`, but the HOST crosstab still FUSES on a mergeable cell aggregator (a weighted mean included, even over a per-element set axis).

## See

- Skills: `overlay-system`, `pairwise-n-sources`, `crosstab-guide`, `op-overlay-pairwise-probit-t`, `op-overlay-prop-z-cell`, `op-test-prop-z`.
