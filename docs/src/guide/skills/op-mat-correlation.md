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
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |
| `fit` | vector fit name | — | Emit from a bound vector fit (a `fits` or `populations` entry: [`compose-fits`](compose-fits.md)), reading no row; by label; its basis governs. Not with `missing` / `max_drop_share` (`fit_and_missing`), groups (`fit_grouped`), a rank method (`fit_unsupported`); a regression's name `PULSE_MODEL_KIND_MISMATCH`. A fit that failed on its data fails this slot alone (its code, `details.fit`). |
| `summary` | int ≥ 1 | — | `summary.top_pairs`: k strongest pairs. |
| `inference` | bool | `false` | `auxiliary.p` / `ci_lower` / `ci_upper`, every method. |
| `alpha` | (0, 1) | `0.05` | Interval level; needs `inference`. |
| `weight` | slot | inherited | Pearson: both kinds; rank: frequency only. `null` opts out. |
| `encoding` | slot | `full` | `upper`: row r holds p − r cells. |

Spec `multiplicity` implies `inference` and adds `auxiliary.p_adjusted` / `significant_adjusted` over the distinct pairs, plus a `multiplicity` echo; family `matrix` (default) or `request`. Raw `p` untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

Integer / float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary`: r (ρ / τ-b under a rank method), diagonal 1; a zero-spread member's row and column are null. `scalars.determinant`; pairwise `auxiliary.n`. `inference`: per pair `auxiliary.p` (two-sided; rank: `cor.test`'s) and `ci_lower` / `ci_upper` (Fisher z; rank: Bonett–Wright / Fieller); diagonal and thin cells null. `vectors.top_pairs` `[{row, col, r, n}]` by |r| desc.

## Reading the output

- `primary.values`: Each off-diagonal cell is the correlation of its row and column members under params.method, from -1 to +1. pearson (default): r, how closely the two follow a straight line together; spearman: rho, r on the members' ranks, how steadily one rises or falls with the other; kendall: tau-b, the share of agreeing minus disagreeing row pairs, tie-adjusted. 0 means no such link. The diagonal is 1.
  - Bands (Cohen (1988), absolute value): very small below 0.1; small 0.1 to 0.3; medium 0.3 to 0.5; large 0.5 and above.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a spec `multiplicity` block (implying `params.inference`; an inherited method reaches only a spec under `params.inference`) adds `p_adjusted` and `significant_adjusted` beside `p`; families `request`, `matrix` (default).
- Streaming: streamable; partial states merge; components `mergeable`.
- Components: `n`, `n_null`, `n_listwise_dropped`; optional `min_pair_n`, `max_pair_n`, `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Each cell is the matching two-field test (Pearson, Spearman, Kendall) over its pair's rows. Weight-0 rows count in `n`, add no mass.
- `inference` rides `auxiliary`: preset `standard` keeps it on the specs that asked, `minimal` drops it. Thin cells: `PULSE_MATRIX_INSUFFICIENT_N` scope `inference`.
- Rank methods: predict `streamable` / `mergeable` false; a probability weight is `PULSE_WEIGHT_UNSUPPORTED`. Kendall τ-b runs smaller than ρ.
- Pairwise r at p ≥ 3 can be non-PSD → `PULSE_MATRIX_NOT_PSD`; also `_INSUFFICIENT_N`, `_ZERO_VARIANCE`, `_LISTWISE_HEAVY_DROP`.
- Refusals and grouping: [`matrix-results`](matrix-results.md).

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`matrix-results`](matrix-results.md), [`op-mat-covariance`](op-mat-covariance.md), [`weighting`](weighting.md)
