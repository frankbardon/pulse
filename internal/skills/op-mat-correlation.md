---
name: op-mat-correlation
description: Correlation matrix of a vector's numeric members — Pearson (default), Spearman or Kendall tau-b via params.method — listwise or pairwise, weighted; one result per spec (per group bucket when grouped); per-pair p-values and confidence intervals on request (params.inference, every method).
kind: operator
category: MAT
operator: MAT_CORRELATION
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot and members as for `op-mat-covariance`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `method` | enum | `pearson` | `pearson`, `spearman` or `kendall`; rank methods: buffered, serial. |
| `missing` | enum | `listwise` | `pairwise`: rank methods re-rank per pair. |
| `max_drop_share` | float | — | 0–1: listwise drop-share warning. |
| `fit` | string | — | A bound vector fit to emit from (`vector-row-scores`). |
| `summary` | object | — | `{"top_pairs": k}`: the k strongest pairs. |
| `inference` | bool | `false` | Per-pair p and CI, every method. |
| `alpha` | float | `0.05` | In (0, 1): CI level; needs `inference`. |
| `weight` | slot | inherited | Rank methods: frequency only. |
| `encoding` | slot | `full` | `upper`: row r holds p − r cells. |

<!-- feature: capability:multiplicity -->
Spec `multiplicity` implies `inference` (`multiplicity-correction`).
<!-- /feature -->

## Inputs

Members: see `## Contract`; `packed_bool` needs `coerce: "binary"`.

## Output

`primary`: r (ρ / τ-b under a rank method), diagonal 1; a zero-spread member's row and column are null. `scalars.determinant`; pairwise `auxiliary.n`. `inference`: per pair `auxiliary.p` (two-sided, `cor.test`) and `ci_lower` / `ci_upper` (Fisher z; rank: Bonett–Wright / Fieller); diagonal and thin cells null. `vectors.top_pairs` `[{row, col, r, n}]` by |r| desc.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Each cell equals the matching two-field test over its pair's rows. Kendall τ-b runs smaller than ρ.
- `inference` rides `auxiliary` (`minimal` drops it); thin cells: `PULSE_MATRIX_INSUFFICIENT_N` scope `inference`.
- Pairwise r at p ≥ 3 can be non-PSD → `PULSE_MATRIX_NOT_PSD`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `vector-row-scores`, `op-mat-covariance`, `weighting`
