---
name: op-mat-covariance
description: Covariance matrix of a vector's numeric members (listwise, sample by default), weighted under frequency and probability weights; one MatrixResult per spec.
kind: operator
category: MAT
operator: MAT_COVARIANCE
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, covariance]
---

Slot: `matrices[i]` `{type, vector | fields, params, weight, encoding}`; members come from `vectors[]` (by name) or inline `fields`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ddof` | 0 \| 1 | `1` | Denominator Σw − ddof. |
| `weight` | slot weight | inherited | Both kinds; `null` opts out. |
| `encoding` | `full` \| `upper` | `full` | `upper`: row r holds p − r cells. |

## Inputs

Integer / float members; `packed_bool` under the vector's `coerce: "binary"`.

## Output

`Response.Matrices[i]`: `primary` `square_symmetric` covariance (`row_keys` = members in axis order), `scalars.determinant` (null unless positive definite). Undefined cells are null.

## Components

No `Response.Components` entry; every figure rides `Response.Matrices[i]`.

## Gotchas

- Listwise: a row with any member null is dropped.
- A row of weight 0 counts but adds no mass; an invalid weight skips the row.
- Unknown `vector` → `PULSE_VECTOR_UNKNOWN`; `vector` and `fields` together → `SERVICE_VALIDATION`.
- Ungrouped and serial only; streamed runs emit it at terminal flush.
- `joins` / chain stage ≥ 1 → `PULSE_MATRIX_UNSUPPORTED_SOURCE`; `crosstab` → `PULSE_MATRIX_HOST_CONFLICT`.

## See

- `pulse_examples_search tags=[covariance]`
- Skills: `request-envelope`, `weighting`
