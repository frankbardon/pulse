```yaml
name: op-mat-correlation
description: Correlation matrix of a vector's numeric members — Pearson (default), Spearman or Kendall tau-b via params.method — listwise or pairwise, weighted; one MatrixResult per spec (per group bucket when grouped); per-pair p-values and confidence intervals on request (params.inference, every method).
kind: operator
category: MAT
operator: MAT_CORRELATION
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
```

Slot: `matrices[i]` `{type, vector | fields, params, weight, encoding}`; members as for [`op-mat-covariance`](op-mat-covariance.md).

## Use when

How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1.

Questions it answers:

- Which of these ten rating items go together most strongly?
- How do the sensor readings line up with one another, pair by pair?

Use something else:

- `TEST_PEARSON_R` when you need a p-value or confidence interval for one pair.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `method` | `pearson` \| `spearman` \| `kendall` | `pearson` | Rank methods: buffered, serial. |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise: per-pair rows (rank methods re-rank per pair). |
| `max_drop_share` | 0–1 | none | Listwise drop-share warning. |
| `summary.top_pairs` | int ≥ 1 | none | k strongest pairs. |
| `inference` | bool | `false` | `auxiliary.p` / `ci_lower` / `ci_upper`, every method. |
| `alpha` | (0, 1) | `0.05` | Interval level; needs `inference`. |
| `weight` | slot weight | inherited | Pearson: both kinds; rank: frequency only. `null` opts out. |
| `encoding` | `full` \| `upper` | `full` | `upper`: row r holds p − r cells. |

Spec `multiplicity` `{method, family, alpha}` (optional) implies `inference` (explicit `false` refused) and adds `auxiliary.p_adjusted` / `significant_adjusted` (1 below its `alpha`, else 0) over the distinct pairs, mirrored, diagonal null, plus a `multiplicity` echo. Family `matrix` (default: one result, m = p(p − 1)/2, per bucket) or `request`. Raw `p` untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

Integer / float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary`: r (ρ / τ-b under a rank method) in [−1, 1], diagonal 1; a zero-spread member's row and column are null. `scalars.determinant` (null unless PD); pairwise `auxiliary.n` (pair N); `warnings`. `inference`: per pair `auxiliary.p` (two-sided; rank: `cor.test`'s, exact when untied and small) and `ci_lower` / `ci_upper` (Fisher z; rank se Bonett–Wright / Fieller, Kendall needs 5 rows; floors skip weight-0 rows); N* per pair (pairwise), Kish under probability weights; diagonal and thin cells null. `vectors.top_pairs` `[{row, col, r, n}]`: by |r| desc, ties in axis order, no diagonal / null pairs.

## Reading the output

- `primary.values`: Each off-diagonal cell is the correlation of its row and column members under params.method, from -1 to +1. pearson (default): r, how closely the two follow a straight line together; spearman: rho, r on the members' ranks, how steadily one rises or falls with the other; kendall: tau-b, the share of agreeing minus disagreeing row pairs, tie-adjusted. 0 means no such link. The diagonal is 1.
  - Bands (Cohen (1988), absolute value): very small below 0.1; small 0.1 to 0.3; medium 0.3 to 0.5; large 0.5 and above.

## Components

`components.matrices[i]`: `n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Each cell is the matching two-field test (Pearson, Spearman, Kendall) over its pair's rows. Weight-0 rows count in `n`, add no mass.
- `inference` rides `auxiliary`: `return` preset `standard` (the MCP default) keeps `p` / `ci_lower` / `ci_upper` (and the pairwise `n`) on the specs that asked; `minimal` drops them. Thin cells: `PULSE_MATRIX_INSUFFICIENT_N` scope `inference`, null p left out of any correction family.
- Rank methods: predict `streamable` / `mergeable` false; a probability weight is `PULSE_WEIGHT_UNSUPPORTED`. Kendall τ-b runs smaller than ρ.
- Pairwise r at p ≥ 3 can be non-PSD (predict `pairwise_psd_risk`) → `PULSE_MATRIX_NOT_PSD`; also `_INSUFFICIENT_N`, `_ZERO_VARIANCE`, `_LISTWISE_HEAVY_DROP`.
- Refusals and grouping: [`matrix-results`](matrix-results.md).

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`matrix-results`](matrix-results.md), [`op-mat-covariance`](op-mat-covariance.md), [`weighting`](weighting.md)
