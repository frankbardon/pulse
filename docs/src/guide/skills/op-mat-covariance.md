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
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |
| `fit` | vector fit name | — | Emit from a bound vector fit (a `fits` or `populations` entry: [`compose-fits`](compose-fits.md)), reading no row; by label; its basis governs. Not with `missing` / `max_drop_share` (`fit_and_missing`), groups (`fit_grouped`); a regression's name `PULSE_MODEL_KIND_MISMATCH`. A fit that failed on its data fails this slot alone (its code, `details.fit`). |
| `weight` | slot | inherited | Both kinds; `null` opts out. |
| `encoding` | slot | `full` | `upper`: row r holds p − r cells. |

## Inputs

Integer/float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary` covariance, `scalars.determinant` (null unless PD), pairwise `auxiliary.n`, `warnings`; undefined cells null. Grouped: one per bucket ([`matrix-results`](matrix-results.md)).

## Components

- `operator.ddof`.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; partial states merge; components `mergeable`.
- Components: `n`, `n_null`, `n_listwise_dropped`, `ddof`; optional `min_pair_n`, `max_pair_n`, `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Warnings: `PULSE_MATRIX_INSUFFICIENT_N`, `_ZERO_VARIANCE` (row stays 0), `_LISTWISE_HEAVY_DROP`, `_NOT_PSD` (pairwise).
- Weight 0 counts in `n`, adds no mass. Predict: `pairwise_psd_risk` (pairwise, p ≥ 2).
- `summary` is refused (`top_pairs`: [`op-mat-correlation`](op-mat-correlation.md)). Refusals and worker invariance: [`matrix-results`](matrix-results.md).

## See

- `pulse_examples_search tags=[covariance]`
- Skills: [`matrix-results`](matrix-results.md), [`weighting`](weighting.md)
