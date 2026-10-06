---
name: op-mat-covariance
description: Covariance matrix of a vector's numeric members (listwise or pairwise, sample by default), weighted under frequency and probability weights; one MatrixResult per spec (per group bucket when grouped).
kind: operator
category: MAT
operator: MAT_COVARIANCE
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, covariance]
---

Slot: `matrices[i]` `{type, vector | fields, params, weight, encoding}`; members: `vectors[]` or inline `fields`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ddof` | 0 \| 1 | `1` | Denominator Σw − ddof. |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise: per-pair rows. |
| `max_drop_share` | 0–1 | none | Listwise drop-share warning. |
| `weight` | slot weight | inherited | Both kinds; `null` opts out. |
| `encoding` | `full` \| `upper` | `full` | `upper`: row r holds p − r cells. |

## Inputs

Integer / float members; `packed_bool` under `coerce: "binary"`.

## Output

`primary` covariance (`row_keys` = members), `scalars.determinant` (null unless PD), pairwise `auxiliary.n`, `warnings`. Undefined cells null. Grouped: one result per non-empty bucket (`group_key`, `group_header`), spec-major in Data order; thin buckets warn.

## Components

`components.matrices[i]`: `n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`; `operator.ddof`.

## Gotchas

- Warnings: `PULSE_MATRIX_INSUFFICIENT_N`, `_ZERO_VARIANCE` (row stays 0), `_LISTWISE_HEAVY_DROP`, `_NOT_PSD` (pairwise).
- Weight 0 counts in `n`, adds no mass. Predict: `pairwise_psd_risk` (pairwise, p ≥ 2).
- Bitwise worker-count invariant; streamed: at terminal flush. `summary` is refused (`top_pairs`: `op-mat-correlation`).
- `joins` / chain stage ≥ 1 → `PULSE_MATRIX_UNSUPPORTED_SOURCE`; `crosstab` → `PULSE_MATRIX_HOST_CONFLICT`.

## See

- `pulse_examples_search tags=[covariance]`
- Skills: `request-envelope`, `weighting`
