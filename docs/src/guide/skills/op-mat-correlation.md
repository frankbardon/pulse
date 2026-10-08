```yaml
name: op-mat-correlation
description: Pearson correlation matrix of a vector's numeric members (listwise or pairwise), weighted under frequency and probability weights; one MatrixResult per spec (per group bucket when grouped), no p-values.
kind: operator
category: MAT
operator: MAT_CORRELATION
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
```

Slot: `matrices[i]` `{type, vector | fields, params, weight, encoding}`; members as for [`op-mat-covariance`](op-mat-covariance.md).

## Use when

How closely every pair in a set of numeric fields follows a straight line together, as one square table of r values from -1 to 1.

Questions it answers:

- Which of these ten rating items go together most strongly?
- How do the sensor readings line up with one another, pair by pair?

Use something else:

- `TEST_PEARSON_R` when you need a p-value or confidence interval for one pair.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise: per-pair rows. |
| `max_drop_share` | 0–1 | none | Listwise drop-share warning. |
| `summary.top_pairs` | int ≥ 1 | none | k strongest pairs. |
| `weight` | slot weight | inherited | Both kinds; `null` opts out. |
| `encoding` | `full` \| `upper` | `full` | `upper`: row r holds p − r cells. |

## Inputs

Integer / float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary`: r clamped to [−1, 1], diagonal 1; a zero-spread member's row and column are null. `scalars.determinant` (null unless PD); pairwise `auxiliary.n` (pair N); `warnings`. `vectors.top_pairs` `[{row, col, r, n}]`: by |r| desc, ties in axis order, no diagonal / null pairs.

## Reading the output

- `primary.values`: Each off-diagonal cell is Pearson r for its row and column members: how closely the two follow a straight line together, from -1 to +1; 0 means no straight-line link. The diagonal is 1.
  - Bands (Cohen (1988), absolute value): very small below 0.1; small 0.1 to 0.3; medium 0.3 to 0.5; large 0.5 and above.
- `scalars.determinant`: The determinant of the correlation table: 1 when no member is linearly related to the others, falling toward 0 as members become linear combinations of one another.

## Components

`components.matrices[i]`: `n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Same arithmetic as the Pearson test; no p-values. Weight-0 rows count in `n`, add no mass.
- Pairwise r at p ≥ 3 can be non-PSD (predict `pairwise_psd_risk`) → `PULSE_MATRIX_NOT_PSD`; also `_INSUFFICIENT_N`, `_ZERO_VARIANCE`, `_LISTWISE_HEAVY_DROP`.
- Refusals and grouping: [`matrix-results`](matrix-results.md).

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`matrix-results`](matrix-results.md), [`op-mat-covariance`](op-mat-covariance.md), [`weighting`](weighting.md)
