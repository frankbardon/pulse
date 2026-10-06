---
name: op-mat-correlation
description: Pearson correlation matrix of a vector's numeric members (listwise), weighted under frequency and probability weights; one MatrixResult per spec, no p-values.
kind: operator
category: MAT
operator: MAT_CORRELATION
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot: `matrices[i]` `{type, vector | fields, weight, encoding}`; members as for `op-mat-covariance`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `weight` | slot weight | inherited | Both kinds; `null` opts out. |
| `encoding` | `full` \| `upper` | `full` | `upper`: row r holds p − r cells. |

No `params` keys; any key is refused.

## Inputs

Integer / float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary`: r clamped to [−1, 1], diagonal 1; a zero-spread member's row and column are null. `scalars.determinant` (null unless positive definite).

## Components

No `Response.Components` entry; figures ride `Response.Matrices[i]`.

## Gotchas

- Same arithmetic as the pairwise Pearson test; no p-values.
- Listwise; weight-0 rows count, add no mass.
- `joins` / chain stage ≥ 1 / `crosstab` refused as for covariance.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `op-mat-covariance`, `weighting`
