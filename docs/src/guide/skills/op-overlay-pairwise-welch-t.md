```yaml
name: op-overlay-pairwise-welch-t
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WELCH_T
description: Intra-matrix axis-pairwise Welch–Satterthwaite t-test on AGG_WELFORD cells.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise, welford-triple]
```

Intra-matrix pairwise on MEANS along one axis of the SAME crosstab: `row` scope pairs row indices per column, `column` pairs column indices per row. Overlays decorate the host; no `Response.Components` (this family READS them).

## Use when

Welch t-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean?

Questions it answers:

- Which age bands differ from each other in average spend, product by product?
- Which pairs of depots differ in average delivery time per month?

Use something else:

- `OVERLAY_PAIRWISE_TWO_MEANS_Z` when every cell is large and you want the normal-curve version.

## Params

`Scope` (enum, required) — `row` or `column`. `Ref` (object, empty) — intra-matrix — leave empty. `params.pair_along_dim` (int, unset) — restrict pairs to same-bucket comparisons on the pair axis.

`n_source` / `p_source` are NOT accepted — n, mean and variance all come from the Welford triple, so either would be a silent no-op. Predict refuses both (`PULSE_OVERLAY_PARAM_MISSING`), for EVERY mode and not just the distinct-key ones; runtime does not, the param being inert (weighted host: see Gotchas). `n_within_depth` stays accepted and inert. Detail: [`pairwise-n-sources`](pairwise-n-sources.md).

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

MATRIX crosstab whose **cell aggregator is `AGG_WELFORD`** + `Response.Components.Crosstab`, from which it reads the `{mean, variance, n}` triple per cell. Non-Welford host → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components-disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values (layout as [`op-overlay-pairwise-prop-z`](op-overlay-pairwise-prop-z.md)). Per pair: `a = v_i/n_i`, `b = v_j/n_j`, `se = sqrt(a + b)`, `t = (m_i - m_j) / se`, `df = (a+b)² / (a²/(n_i-1) + b²/(n_j-1))` (Welch–Satterthwaite), two-sided via the `studentTTwoSidedP` helper the t-tests use.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided Welch t-test p-value for one pair of means, with Welch-Satterthwaite degrees of freedom; it is absent when a leg is unreadable or the test is degenerate.
  - Caveat: Each mean should be roughly normal: safe for big cells, risky for small skewed ones.

## Gotchas

- Weighted host (both kinds, any source): legs read N* = `sum_weights` (frequency) / `n_eff` (probability), variance from `m2` on w*, df on N*; `Summary.Parameters` adds `sum_weights` (+ `n_eff`). There an unweighted-count `n_source` (and a weight-sum one under probability) is `PROCESSING_CONFIG` in predict AND runtime ([`pairwise-n-sources`](pairwise-n-sources.md)).
- Either leg with `n <= 1` skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`).
- RAW p-values only — direction / thresholds are the embedder's job.
- Buffered (inferential) — and so is the HOST: the `AGG_WELFORD` cell is non-mergeable, so `CanFuseCrosstab` rejects on the cell-aggregator arm. Expected (`TestCrosstabWelfordCell_StaysBufferedWithCorrectOverlays`).

## See

- Skills: [`overlay-system`](overlay-system.md), [`pairwise-n-sources`](pairwise-n-sources.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-pairwise-two-means-z`](op-overlay-pairwise-two-means-z.md), [`op-agg-welford`](op-agg-welford.md), [`op-test-welch`](op-test-welch.md).
