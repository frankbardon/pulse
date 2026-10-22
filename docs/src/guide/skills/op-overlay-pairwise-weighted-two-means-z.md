```yaml
name: op-overlay-pairwise-weighted-two-means-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z
description: Intra-matrix axis-pairwise two-means z-test on weighted-mean cells under a required n_basis (weights | kish).
type: reference
applies_to: process
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

`Scope` (required) — `row` or `column`. `Ref` empty. `params.n_basis` (REQUIRED, no default) — `weights` or `kish`. `params.pair_along_dim` (int) — same-bucket pairs. `n_source` / `p_source` / bad `n_basis` → `PULSE_OVERLAY_PARAM_MISSING`; `weights` on a probability-weighted cell (incl. `weight_field`) → `PROCESSING_CONFIG`. `params.letters` (object, unset) — `{}` or `{"alpha": a}`, `0 < a < 1` (else `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime): adds the `letters` part.


## Host shape

MATRIX crosstab, weighted-moment cell: `AGG_WEIGHTED_MEAN` or a weighted `AGG_AVERAGE`. Reads `weighted_mean`, `m2_weighted`, `sum_weights`, `sum_weights_sq` — never floor `n`. Else `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`; components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED` (predict and runtime, after the `n_basis` / selector checks).

## Output

Pair × opposite-axis two-sided p-values. `weights`: `var = m2/(Σw−1)`, `n = Σw`. `kish`: `var = m2/(Σw−Σw²/Σw)`, `n = n_eff`. `z = (m_i−m_j)/sqrt(var_i/n_i + var_j/n_j)`, `p = 2Φ(−|z|)`. `payload.parts.letters` (with `params.letters`): per host cell of a compared index `{letter, group, higher, above}` — the letters its weighted mean beats (sign of `z`) and the significant subset (raw p < `0.05` or `alpha`; `significant_adjusted` under a correction).

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided z-test p-value for one pair of weighted means; params.n_basis sets the sample size (sum of weights, or Kish's effective sample size).
- `parts.letters.cells.value`: Significance letters for this host cell: its column's (or row's) letter, and in above the letters of the same-group columns it is significantly higher than here.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `row`, `column`, `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Skips (`PULSE_OVERLAY_REF_ZERO`): `weights` leg `Σw ≤ 1`; `kish` leg with one weighted row; zero SE.
- Mergeable cell: stays fused. `n_basis` sets n: `weights` needs frequency weights (Σw is no sample size under probability); probability → `kish`.

## See

- Skills: [`op-overlay-pairwise-two-means-z`](op-overlay-pairwise-two-means-z.md), [`op-agg-weighted-mean`](op-agg-weighted-mean.md), [`overlay-system`](overlay-system.md).
