---
name: op-mat-correlation
description: Correlation matrix of a vector's numeric members — Pearson (default), Spearman or Kendall tau-b via params.method — listwise or pairwise, weighted; one MatrixResult per spec (per group bucket when grouped); per-pair p-values and confidence intervals on request (params.inference, every method).
kind: operator
category: MAT
operator: MAT_CORRELATION
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot: `matrices[i]` `{type, vector | fields, params, weight, encoding}`; members as for `op-mat-covariance`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `method` | `pearson` \| `spearman` \| `kendall` | `pearson` | Rank methods: buffered, serial. |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise: per-pair rows (rank methods re-rank per pair). |
| `max_drop_share` | 0–1 | none | Listwise drop-share warning. |
| `summary.top_pairs` | int ≥ 1 | none | k strongest pairs. |
| `fit` | vector fit name | none | Emit from a bound vector fit (a `fits` or `populations` entry: `compose-fits`), reading no row; by label; its basis governs. Not with `missing` / `max_drop_share` (`fit_and_missing`), groups (`fit_grouped`), a rank method (`fit_unsupported`); a regression's name `PULSE_MODEL_KIND_MISMATCH`. A fit that failed on its data fails this slot alone (its code, `details.fit`). |
| `inference` | bool | `false` | `auxiliary.p` / `ci_lower` / `ci_upper`, every method. |
| `alpha` | (0, 1) | `0.05` | Interval level; needs `inference`. |
| `weight` | slot weight | inherited | Pearson: both kinds; rank: frequency only. `null` opts out. |
| `encoding` | `full` \| `upper` | `full` | `upper`: row r holds p − r cells. |

<!-- feature: capability:multiplicity -->
Spec `multiplicity` implies `inference` and adds `auxiliary.p_adjusted` / `significant_adjusted` over the distinct pairs, plus a `multiplicity` echo; family `matrix` (default) or `request`. Raw `p` untouched (`multiplicity-correction`).
<!-- /feature -->

## Inputs

Integer / float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary`: r (ρ / τ-b under a rank method), diagonal 1; a zero-spread member's row and column are null. `scalars.determinant`; pairwise `auxiliary.n`. `inference`: per pair `auxiliary.p` (two-sided; rank: `cor.test`'s) and `ci_lower` / `ci_upper` (Fisher z; rank: Bonett–Wright / Fieller); diagonal and thin cells null. `vectors.top_pairs` `[{row, col, r, n}]` by |r| desc.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Each cell is the matching two-field test (Pearson, Spearman, Kendall) over its pair's rows. Weight-0 rows count in `n`, add no mass.
- `inference` rides `auxiliary`: preset `standard` keeps it on the specs that asked, `minimal` drops it. Thin cells: `PULSE_MATRIX_INSUFFICIENT_N` scope `inference`.
- Rank methods: predict `streamable` / `mergeable` false; a probability weight is `PULSE_WEIGHT_UNSUPPORTED`. Kendall τ-b runs smaller than ρ.
- Pairwise r at p ≥ 3 can be non-PSD → `PULSE_MATRIX_NOT_PSD`; also `_INSUFFICIENT_N`, `_ZERO_VARIANCE`, `_LISTWISE_HEAVY_DROP`.
- Refusals and grouping: `matrix-results`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `op-mat-covariance`, `weighting`
