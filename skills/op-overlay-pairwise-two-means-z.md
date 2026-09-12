---
name: op-overlay-pairwise-two-means-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_TWO_MEANS_Z
description: Intra-matrix axis-pairwise two-means z-test on AGG_WELFORD cells (normal-CDF tail, no df).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise, welford-triple]
---

Intra-matrix pairwise on MEANS: one host cell's mean against another ALONG one axis of the SAME crosstab, reading the `{mean, variance, n}` Welford triple from `Response.Components.Crosstab.CellComponents`. `row` scope pairs row indices per column; `column` pairs column indices per row. Normal-CDF sibling of `OVERLAY_PAIRWISE_WELCH_T` — same standard error, no Satterthwaite df. Overlays decorate the host; they emit no `Response.Components` (this family READS them).

## Params

`Scope` (enum, required) — `row` or `column`. `Ref` (object, empty) — intra-matrix — leave empty. `params.pair_along_dim` (int, unset) — restrict pairs to same-bucket comparisons on the pair axis.

`n_source` / `p_source` ignored — n and moments come from the Welford triple.

## Host shape

MATRIX crosstab whose **cell aggregator is `AGG_WELFORD`** + `Response.Components.Crosstab`. Non-Welford host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components-disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values (layout as `op-overlay-pairwise-prop-z`). Per pair: `a = v_i/n_i`, `b = v_j/n_j`, `se = sqrt(a + b)`, `z = (m_i - m_j) / se`, `p = 2 * (1 - Φ(|z|))` via the `standardNormalCDF` helper backing `TEST_Z_TWO_SAMPLE`.

## Gotchas

- Either leg with `n <= 1` skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`).
- Use `OVERLAY_PAIRWISE_WELCH_T` when small-sample df correction matters; this kind assumes the normal approximation.
- RAW p-values only — direction / thresholds are the embedder's job.
- Buffered (inferential) — and so is the HOST: the `AGG_WELFORD` cell is non-mergeable, so `CanFuseCrosstab` rejects on the cell-aggregator arm. Expected (`TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays`).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-welch-t`, `op-agg-welford`, `op-test-z-two-sample`.
