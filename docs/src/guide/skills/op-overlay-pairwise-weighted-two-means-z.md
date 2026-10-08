```yaml
name: op-overlay-pairwise-weighted-two-means-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z
description: Intra-matrix axis-pairwise two-means z-test on weighted-mean cells under a required n_basis (weights | kish).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
```

Weighted pairwise two-means z: pairs rows (`row` scope) or columns (`column`) on weighted means. Reads `Response.Components`.

## Use when

Large-sample z-tests between every pair of rows (or columns) of one crosstab of weighted averages, with a sample-size basis you choose.

Questions it answers:

- Which weighted segments differ from each other in average satisfaction?
- Which regions differ in weighted average spend once survey weights are applied?

Use something else:

- `OVERLAY_PAIRWISE_WELCH_T` when the cells are unweighted averages.

## Params

`Scope` (required) — `row` or `column`. `Ref` empty. `params.n_basis` (REQUIRED, no default) — `weights` or `kish`. `params.pair_along_dim` (int) — same-bucket pairs. `n_source` / `p_source` / bad `n_basis` → `PULSE_OVERLAY_PARAM_MISSING`; `weights` on a probability-weighted cell (incl. `weight_field`) → `PROCESSING_CONFIG`.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

MATRIX crosstab, weighted-moment cell: `AGG_WEIGHTED_MEAN` or a weighted `AGG_AVERAGE`. Reads `weighted_mean`, `m2_weighted`, `sum_weights`, `sum_weights_sq` — never floor `n`. Else `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

Pair × opposite-axis two-sided p-values. `weights`: `var = m2/(Σw−1)`, `n = Σw`. `kish`: `var = m2/(Σw−Σw²/Σw)`, `n = n_eff`. `z = (m_i−m_j)/sqrt(var_i/n_i + var_j/n_j)`, `p = 2Φ(−|z|)`.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided z-test p-value for one pair of weighted means; params.n_basis sets the sample size (sum of weights, or Kish's effective sample size).
  - Caveat: It accounts for weighting only, not clustering or other design effects, so with such designs the p-values come out too small.

## Gotchas

- Skips (`PULSE_OVERLAY_REF_ZERO`): `weights` leg `Σw ≤ 1`; `kish` leg with one weighted row; zero SE.
- Mergeable cell: stays fused. `n_basis` sets n: `weights` needs frequency weights (Σw is no sample size under probability); probability → `kish`.

## See

- Skills: [`op-overlay-pairwise-two-means-z`](op-overlay-pairwise-two-means-z.md), [`op-agg-weighted-mean`](op-agg-weighted-mean.md), [`overlay-system`](overlay-system.md).
