---
name: op-overlay-pairwise-welch-t
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WELCH_T
description: Intra-matrix axis-pairwise Welch–Satterthwaite t-test on AGG_WELFORD cells.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise, welford-triple]
---

Intra-matrix pairwise on MEANS along one axis of the SAME crosstab: `row` scope pairs row indices per column, `column` pairs column indices per row. Overlays decorate the host; no `Response.Components` (this family READS them).

## Params

`Scope` (enum, required) — `row` or `column`. `Ref` (object, empty) — intra-matrix — leave empty. `params.pair_along_dim` (int, unset) — restrict pairs to same-bucket comparisons on the pair axis.

`n_source` / `p_source` ignored — n and moments come from the Welford triple.

## Host shape

MATRIX crosstab whose **cell aggregator is `AGG_WELFORD`** + `Response.Components.Crosstab`, from which it reads the `{mean, variance, n}` triple per cell. Non-Welford host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components-disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values (layout as `op-overlay-pairwise-prop-z`). Per pair: `a = v_i/n_i`, `b = v_j/n_j`, `se = sqrt(a + b)`, `t = (m_i - m_j) / se`, `df = (a+b)² / (a²/(n_i-1) + b²/(n_j-1))` (Welch–Satterthwaite), two-sided via the `studentTTwoSidedP` helper backing `TEST_T` / `TEST_WELCH`.

## Gotchas

- Either leg with `n <= 1` skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`).
- Normal-CDF sibling is `OVERLAY_PAIRWISE_TWO_MEANS_Z` (same SE, no df adjustment).
- RAW p-values only — direction / thresholds are the embedder's job.
- Buffered (inferential) — and so is the HOST: the `AGG_WELFORD` cell is non-mergeable, so `CanFuseCrosstab` rejects on the cell-aggregator arm. Expected (`TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays`).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-two-means-z`, `op-agg-welford`, `op-test-welch`.
