---
name: op-mat-correlation
description: Pearson correlation matrix of a vector's numeric members (listwise or pairwise), weighted under frequency and probability weights; one MatrixResult per spec, no p-values.
kind: operator
category: MAT
operator: MAT_CORRELATION
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot: `matrices[i]` `{type, vector | fields, params, weight, encoding}`; members as for `op-mat-covariance`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise: per-pair rows. |
| `max_drop_share` | 0–1 | none | Listwise drop-share warning. |
| `weight` | slot weight | inherited | Both kinds; `null` opts out. |
| `encoding` | `full` \| `upper` | `full` | `upper`: row r holds p − r cells. |

## Inputs

Integer / float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary`: r clamped to [−1, 1], diagonal 1; a zero-spread member's row and column are null. `scalars.determinant` (null unless PD); pairwise `auxiliary.n` (pair N); `warnings`.

## Components

No `Response.Components` entry; figures ride `Response.Matrices[i]`.

## Gotchas

- Same arithmetic as the Pearson test; no p-values.
- Weight-0 rows count, add no mass.
- Pairwise r can be non-PSD → `PULSE_MATRIX_NOT_PSD`; also `_INSUFFICIENT_N`, `_ZERO_VARIANCE`, `_LISTWISE_HEAVY_DROP`.
- `joins` / chain stage ≥ 1 / `crosstab` refused as for covariance.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `op-mat-covariance`, `weighting`
