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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ddof` | 0 \| 1 | `1` | Denominator Σw − ddof. |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise: per-pair rows. |
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |
| `fit` | vector fit name | — | Emit from a bound vector fit (a `fits` or `populations` entry: `compose-fits`), reading no row; by label; its basis governs. Not with `missing` / `max_drop_share` (`fit_and_missing`), groups (`fit_grouped`); a regression's name `PULSE_MODEL_KIND_MISMATCH`. A fit that failed on its data fails this slot alone (its code, `details.fit`). |
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
- `summary` is refused (`top_pairs`: `op-mat-correlation`). Refusals and worker invariance: `matrix-results`.

## See

- `pulse_examples_search tags=[covariance]`
- Skills: `matrix-results`, `weighting`
