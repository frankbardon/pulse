---
name: op-mat-reliability
description: Scale reliability of a battery — Cronbach's alpha, standardized alpha, McDonald's omega (one-factor minres), mean inter-item r, per-item item-total r / alpha if deleted / mean / sd — with reverse-keyed items flipped before the fold; listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_RELIABILITY
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot and members as for `op-mat-covariance`; at least 2 items.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `reverse` | [fields] | — | Reverse-keyed items: x' = min + max − x before the fold. |
| `scale_min` | float | — | Battery range, with `scale_max`; required with `reverse` unless the vector declares `kind: "scale"` bounds. |
| `scale_max` | float | — | See `scale_min`. |
| `repair` | `nearest` | — | Fit omega on the nearest correlation matrix. |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise adds `auxiliary.n` (`matrix-results`). |
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |
| `fit` | string | — | Emit from a bound vector fit (`vector-row-scores`). |

## Inputs

Items: see `## Contract`.

## Output

`primary`: inter-item correlation. `scalars`: `alpha`, `alpha_standardized`, `omega`, `mean_inter_item_r`. `vectors` (axis order): `item_total_r` (corrected), `alpha_if_deleted` (null at 2 items), `item_mean`, `item_sd`. No p-values.

omega = (Σλ)² / ((Σλ)² + Σψ) from the minres fit — the model form, not psych's `omega.tot` (lower on a misfit battery).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `reverse` without the range, or an item value outside it: `PROCESSING_CONFIG` (never clamped or inferred).
- omega null + warning: 2 items (`PULSE_MATRIX_NOT_IDENTIFIED`), a Heywood fit (`PULSE_MATRIX_HEYWOOD`), non-PSD pairwise input without `repair` (`PULSE_MATRIX_NOT_PSD`). Alpha always computes.
- Negative alpha usually means an unreversed item.
- Pairwise + probability weight: a pair with n_eff ≤ 1 is null, as is every figure it reaches.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `op-mat-correlation`, `weighting`, `vector-metrics`
