```yaml
name: op-mat-covariance
description: Covariance matrix of a vector's numeric members (listwise or pairwise, sample by default), weighted under frequency and probability weights; one MatrixResult per spec (per group bucket when grouped).
kind: operator
category: MAT
operator: MAT_COVARIANCE
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, covariance]
```

Slot: `matrices[i]` `{type, vector | fields, params, weight, encoding}`; members: `vectors[]` or inline `fields`.

## Use when

How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal.

Questions it answers:

- How do these five rating scales vary together across respondents?
- What is the covariance table of the sensor readings, to feed a later model?

Use something else:

- `TEST_PEARSON_R` when you want how closely two numeric fields follow a straight line together, on a scale from -1 to 1.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ddof` | 0 \| 1 | `1` | Denominator Σw − ddof. |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise: per-pair rows. |
| `max_drop_share` | 0–1 | none | Listwise drop-share warning. |
| `weight` | slot weight | inherited | Both kinds; `null` opts out. |
| `encoding` | `full` \| `upper` | `full` | `upper`: row r holds p − r cells. |

## Inputs

Integer/float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary` covariance, `scalars.determinant` (null unless PD), pairwise `auxiliary.n`, `warnings`; undefined cells null. Grouped: one per bucket ([`matrix-results`](matrix-results.md)).

## Components

`components.matrices[i]`: `n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`; `operator.ddof`.

## Gotchas

- Warnings: `PULSE_MATRIX_INSUFFICIENT_N`, `_ZERO_VARIANCE` (row stays 0), `_LISTWISE_HEAVY_DROP`, `_NOT_PSD` (pairwise).
- Weight 0 counts in `n`, adds no mass. Predict: `pairwise_psd_risk` (pairwise, p ≥ 2).
- `summary` is refused (`top_pairs`: [`op-mat-correlation`](op-mat-correlation.md)). Refusals and worker invariance: [`matrix-results`](matrix-results.md).

## See

- `pulse_examples_search tags=[covariance]`
- Skills: [`matrix-results`](matrix-results.md), [`weighting`](weighting.md)
