---
name: op-mat-covariance
description: Covariance matrix of a vector's numeric members (listwise or pairwise, sample by default), weighted under frequency and probability weights; one result per spec (per group bucket when grouped).
kind: operator
category: MAT
operator: MAT_COVARIANCE
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, covariance]
---

Slot: `matrices[i]` `{type, vector | fields, params, weight, encoding}`; members: `vectors[]` or inline `fields`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ddof` | enum | `1` | `0` or `1`: denominator Σw − ddof. |
| `missing` | enum | `listwise` | `pairwise`: per-pair rows. |
| `max_drop_share` | float | — | 0–1: listwise drop-share warning. |
| `fit` | string | — | Emit from a bound vector fit's listwise state, reading no row (`vector-row-scores`). |
| `weight` | slot | inherited | Both kinds; `null` opts out. |
| `encoding` | slot | `full` | `upper`: row r holds p − r cells. |

## Inputs

Integer/float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary` covariance, `scalars.determinant` (null unless PD), pairwise `auxiliary.n`, `warnings`; undefined cells null. Grouped: one per bucket (`matrix-results`).

## Components

- `operator.ddof`.

<!-- generated: contract -->

## Gotchas

- Warnings: `PULSE_MATRIX_INSUFFICIENT_N`, `_ZERO_VARIANCE` (row stays 0), `_LISTWISE_HEAVY_DROP`, `_NOT_PSD` (pairwise).
- Weight 0 counts in `n`, adds no mass. Predict: `pairwise_psd_risk` (pairwise, p ≥ 2).
- `summary` is refused `SERVICE_VALIDATION` (`top_pairs`: `op-mat-correlation`). Refusals and worker invariance: `matrix-results`.

## See

- `pulse_examples_search tags=[covariance]`
- Skills: `matrix-results`, `vector-row-scores`, `weighting`
