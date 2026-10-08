```yaml
name: op-overlay-pairwise-two-means-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_TWO_MEANS_Z
description: Intra-matrix axis-pairwise two-means z-test on AGG_WELFORD cells (normal-CDF tail, no df).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise, welford-triple]
```

Intra-matrix pairwise on MEANS along one axis of the SAME crosstab: `row` scope pairs row indices per column, `column` pairs column indices per row. Normal-CDF sibling of the pairwise Welch t — same standard error, no Satterthwaite df. Overlays decorate the host; no `Response.Components` (this family READS them).

## Use when

Large-sample z-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean?

Questions it answers:

- With thousands of respondents per cell, which segments differ in average rating?
- Which pairs of high-volume stores differ in average basket size?

Use something else:

- `OVERLAY_PAIRWISE_WELCH_T` when any cell is small; the t-based version is more honest there.

## Params

`Scope` (enum, required) — `row` or `column`. `Ref` (object, empty) — intra-matrix — leave empty. `params.pair_along_dim` (int, unset) — restrict pairs to same-bucket comparisons on the pair axis.

`n_source` / `p_source` are NOT accepted — n, mean and variance all come from the Welford triple, so either would be a silent no-op. Predict refuses both (`PULSE_OVERLAY_PARAM_MISSING`), for EVERY mode and not just the distinct-key ones; runtime does not, the param being inert. `n_within_depth` stays accepted and inert. Detail: [`pairwise-n-sources`](pairwise-n-sources.md).

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

MATRIX crosstab whose **cell aggregator is `AGG_WELFORD`** + `Response.Components.Crosstab`, from which it reads the `{mean, variance, n}` triple per cell. Non-Welford host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components-disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values  (layout as [`op-overlay-pairwise-prop-z`](op-overlay-pairwise-prop-z.md)). Per pair: `a = v_i/n_i`, `b = v_j/n_j`, `se = sqrt(a + b)`, `z = (m_i - m_j) / se`, `p = 2 * Φ(-|z|)` via the `normalTwoSidedP` helper the two-sample z-test uses.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided z-test p-value for one pair of means, read from the normal curve; it is absent when a leg is unreadable or the test is degenerate.
  - Caveat: With small cells the normal curve gives p-values that are too small; OVERLAY_PAIRWISE_WELCH_T is the safer reading there.

## Gotchas

- Never weighted: a row weight reaching the overlay slot (request, its own `weight`, or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason` and `details.alternative`: use `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` for weighted means; set `"weight": null` on the overlay to run it unweighted.
- Either leg with `n <= 1` skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`).
- Assumes the normal approximation: no small-sample df correction.
- `n_basis` here is inert; predict refuses it.
- RAW p-values only — direction / thresholds are the embedder's job.
- Buffered (inferential) — and so is the HOST: the `AGG_WELFORD` cell is non-mergeable, so `CanFuseCrosstab` rejects on the cell-aggregator arm. Expected (`TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays`).

## See

- Skills: [`overlay-system`](overlay-system.md), [`pairwise-n-sources`](pairwise-n-sources.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-pairwise-welch-t`](op-overlay-pairwise-welch-t.md), [`op-agg-welford`](op-agg-welford.md), [`op-test-z-two-sample`](op-test-z-two-sample.md).
