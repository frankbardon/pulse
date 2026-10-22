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
| `scale_min` | float | — | Battery range; both or neither, required with `reverse`. Absent: a `kind: "scale"` vector's bounds (`vector-metrics`). |
| `scale_max` | float | — | Battery range; both or neither, required with `reverse`. Absent: a `kind: "scale"` vector's bounds (`vector-metrics`). |
| `repair` | `nearest` | — | Fit omega on the nearest correlation matrix. |
| `missing` | `listwise` \| `pairwise` | `listwise` | listwise drops a row with any member null; pairwise computes each pair over the rows where both are present and adds auxiliary.n (pairwise N). |
| `max_drop_share` | float | — | Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1). |
| `fit` | string | — | A bound vector fit's name (a Compose fits entry over one MAT_* or fitted-attribute battery): the matrix is emitted from that fit's listwise state, folding no row; matched to the members BY LABEL; its own weight basis governs. |

## Inputs

Integer / float items.

## Output

`primary`: inter-item correlation. `scalars`: `alpha`, `alpha_standardized`, `omega`, `mean_inter_item_r`. `vectors` (axis order): `item_total_r` (corrected), `alpha_if_deleted` (null at 2 items), `item_mean`, `item_sd`. Pairwise `auxiliary.n`; `warnings`. No p-values.

omega = (Σλ)² / ((Σλ)² + Σψ) from the minres fit (the model form, not psych::omega's observed-total `omega.tot`, which reads lower on a misfit battery).

<!-- generated: reading-the-output -->

## Components

- `iterations`, `converged` when the omega fit ran.

<!-- generated: contract -->

## Gotchas

- `reverse` without the range, or an item value outside it: `PROCESSING_CONFIG` (never clamped or inferred).
- omega null + warning: 2 items (`PULSE_MATRIX_NOT_IDENTIFIED`), a Heywood fit (`PULSE_MATRIX_HEYWOOD`), non-PSD pairwise input without `repair` (`PULSE_MATRIX_NOT_PSD`). Alpha always computes.
- Negative alpha usually means an unreversed item.
- Pairwise + probability weight: each covariance entry takes its own pair's n_eff; a pair with n_eff ≤ 1 is null, and so is every figure it reaches.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `op-mat-correlation`, `weighting`, `vector-metrics`
